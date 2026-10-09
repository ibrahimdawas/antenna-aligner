package router

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HuaweiClient implements SignalSource for the B310s-22 HiLink API.
type HuaweiClient struct {
	mu        sync.Mutex
	routerURL string
	username  string
	password  string
	client    *http.Client
	sesInfo   string // session cookie value
	token     string // CSRF token
	loggedIn  bool
}

// NewHuaweiClient creates a client for the given router.
func NewHuaweiClient(routerURL, username, password string) *HuaweiClient {
	return &HuaweiClient{
		routerURL: strings.TrimRight(routerURL, "/"),
		username:  username,
		password:  password,
		client: &http.Client{
			Timeout: 3 * time.Second,
			// Don't follow redirects automatically
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// --- XML response types ---

type signalResponse struct {
	XMLName xml.Name `xml:"response"`
	Rsrp    string   `xml:"rsrp"`
	Rsrq    string   `xml:"rsrq"`
	Sinr    string   `xml:"sinr"`
	Rssi    string   `xml:"rssi"`
	CellID  string   `xml:"cell_id"`
	PCI     string   `xml:"pci"`
	Band    string   `xml:"band"`
	// Some firmware uses different element names
	RSRP2   string `xml:"Rsrp"`
	RSRQ2   string `xml:"Rsrq"`
	SINR2   string `xml:"Sinr"`
	RSSI2   string `xml:"Rssi"`
	CellID2 string `xml:"Cell_id"`
	PCI2    string `xml:"Pci"`
	Band2   string `xml:"Band"`
}

type errorResponse struct {
	XMLName xml.Name `xml:"error"`
	Code    string   `xml:"code"`
	Message string   `xml:"message"`
}

type sesTokResponse struct {
	XMLName xml.Name `xml:"response"`
	SesInfo string   `xml:"SesInfo"`
	TokInfo string   `xml:"TokInfo"`
}

type loginResponse struct {
	XMLName xml.Name `xml:"response"`
	Result  string   `xml:"result"`
}

// FetchSample reads signal metrics from the router.
func (c *HuaweiClient) FetchSample() (*Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try without login first (if not already logged in)
	if !c.loggedIn {
		sample, err := c.fetchSignal()
		if err == nil {
			return sample, nil
		}
		// Check if it's an auth error
		if !isAuthError(err) {
			return nil, fmt.Errorf("signal fetch failed: %w", err)
		}
		log.Println("[router] Unauthenticated request returned auth error, attempting login...")
		// Need to login
		if loginErr := c.login(); loginErr != nil {
			return nil, fmt.Errorf("login failed: %w", loginErr)
		}
	}

	// Try with session
	sample, err := c.fetchSignal()
	if err == nil {
		return sample, nil
	}
	if !isAuthError(err) {
		return nil, fmt.Errorf("signal fetch failed: %w", err)
	}

	// Auth error mid-run, re-login (max 1 retry)
	log.Println("[router] Auth error mid-session, re-logging in...")
	c.loggedIn = false
	if loginErr := c.login(); loginErr != nil {
		return nil, fmt.Errorf("re-login failed: %w", loginErr)
	}
	return c.fetchSignal()
}

func (c *HuaweiClient) Close() {}

func (c *HuaweiClient) fetchSignal() (*Sample, error) {
	req, err := http.NewRequest("GET", c.routerURL+"/api/device/signal", nil)
	if err != nil {
		return nil, err
	}
	c.addHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.updateTokenFromResponse(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check for error response
	if errResp := parseError(body); errResp != nil {
		return nil, errResp
	}

	return parseSignalXML(body)
}

func parseSignalXML(body []byte) (*Sample, error) {
	var sig signalResponse
	if err := xml.Unmarshal(body, &sig); err != nil {
		return nil, fmt.Errorf("XML parse error: %w", err)
	}

	s := &Sample{Time: time.Now()}

	// Use whichever field is populated (handle case variations)
	rsrpStr := firstNonEmpty(sig.Rsrp, sig.RSRP2)
	rsrqStr := firstNonEmpty(sig.Rsrq, sig.RSRQ2)
	sinrStr := firstNonEmpty(sig.Sinr, sig.SINR2)
	rssiStr := firstNonEmpty(sig.Rssi, sig.RSSI2)

	s.RSRP = ParseDBValue(rsrpStr)
	s.RSRQ = ParseDBValue(rsrqStr)
	s.SINR = ParseDBValue(sinrStr)
	s.RSSI = ParseDBValue(rssiStr)
	s.CellID = firstNonEmpty(sig.CellID, sig.CellID2)
	s.PCI = firstNonEmpty(sig.PCI, sig.PCI2)
	s.Band = firstNonEmpty(sig.Band, sig.Band2)

	log.Printf("[router] Signal: RSRP=%v RSRQ=%v SINR=%v RSSI=%v CellID=%s Band=%s",
		ptrStr(s.RSRP), ptrStr(s.RSRQ), ptrStr(s.SINR), ptrStr(s.RSSI), s.CellID, s.Band)
	return s, nil
}

func (c *HuaweiClient) login() error {
	// Step 1: Get session token
	req, err := http.NewRequest("GET", c.routerURL+"/api/webserver/SesTokInfo", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("SesTokInfo request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var stResp sesTokResponse
	if err := xml.Unmarshal(body, &stResp); err != nil {
		return fmt.Errorf("SesTokInfo parse error: %w", err)
	}
	c.sesInfo = stResp.SesInfo
	c.token = stResp.TokInfo
	log.Printf("[router] Got session token (token prefix: %s...)", safePrefix(c.token, 8))

	// Step 2: Try password_type 4 (SHA256 hashed)
	pw4 := hashPasswordType4(c.username, c.password, c.token)
	err = c.doLogin(pw4, "4")
	if err == nil {
		log.Println("[router] Login successful with password_type 4")
		return nil
	}
	log.Printf("[router] password_type 4 login failed: %v, trying type 3...", err)

	// Refresh token (it rotates)
	req2, _ := http.NewRequest("GET", c.routerURL+"/api/webserver/SesTokInfo", nil)
	resp2, err2 := c.client.Do(req2)
	if err2 == nil {
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		var st2 sesTokResponse
		if xml.Unmarshal(body2, &st2) == nil {
			c.sesInfo = st2.SesInfo
			c.token = st2.TokInfo
		}
	}

	// Step 3: Try password_type 3 (plain base64)
	pw3 := base64.StdEncoding.EncodeToString([]byte(c.password))
	err = c.doLogin(pw3, "3")
	if err == nil {
		log.Println("[router] Login successful with password_type 3")
		return nil
	}

	return fmt.Errorf("all login methods failed: %w", err)
}

func (c *HuaweiClient) doLogin(encodedPw, pwType string) error {
	loginBody := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><request><Username>%s</Username><Password>%s</Password><password_type>%s</password_type></request>`,
		c.username, encodedPw, pwType,
	)

	req, err := http.NewRequest("POST", c.routerURL+"/api/user/login", strings.NewReader(loginBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Cookie", c.sesInfo)
	req.Header.Set("__RequestVerificationToken", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.updateTokenFromResponse(resp)

	// Update session cookie from response
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "SessionID" {
			c.sesInfo = cookie.Name + "=" + cookie.Value
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if errResp := parseError(body); errResp != nil {
		return errResp
	}

	c.loggedIn = true
	return nil
}

func (c *HuaweiClient) addHeaders(req *http.Request) {
	if c.sesInfo != "" {
		req.Header.Set("Cookie", c.sesInfo)
	}
	if c.token != "" {
		req.Header.Set("__RequestVerificationToken", c.token)
	}
}

func (c *HuaweiClient) updateTokenFromResponse(resp *http.Response) {
	// Tokens rotate on every response
	for _, name := range []string{"__RequestVerificationToken", "__RequestVerificationTokenone", "__RequestVerificationTokentwo"} {
		if t := resp.Header.Get(name); t != "" {
			c.token = t
			break
		}
	}
	// Update session cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "SessionID" {
			c.sesInfo = cookie.Name + "=" + cookie.Value
		}
	}
}

// hashPasswordType4 computes the Huawei type-4 password hash:
// base64( hex( sha256( username + base64( hex( sha256(password) ) ) + token ) ) )
func hashPasswordType4(username, password, token string) string {
	// Inner: sha256(password)
	h1 := sha256.Sum256([]byte(password))
	h1Hex := hex.EncodeToString(h1[:])
	h1B64 := base64.StdEncoding.EncodeToString([]byte(h1Hex))

	// Outer: sha256( username + h1B64 + token )
	combined := username + h1B64 + token
	h2 := sha256.Sum256([]byte(combined))
	h2Hex := hex.EncodeToString(h2[:])
	return base64.StdEncoding.EncodeToString([]byte(h2Hex))
}

// --- helpers ---

type authError struct {
	code    string
	message string
}

func (e *authError) Error() string {
	return fmt.Sprintf("router error %s: %s", e.code, e.message)
}

func parseError(body []byte) *authError {
	var errResp errorResponse
	if xml.Unmarshal(body, &errResp) == nil && errResp.Code != "" {
		return &authError{code: errResp.Code, message: errResp.Message}
	}
	return nil
}

func isAuthError(err error) bool {
	ae, ok := err.(*authError)
	if !ok {
		return false
	}
	return ae.code == "125002" || ae.code == "100003" || ae.code == "125001"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func ptrStr(p *float64) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%.1f", *p)
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
