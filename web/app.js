// Antenna Aligner - Frontend Application
// Vanilla JS, no frameworks, no build step.

(function () {
    'use strict';

    // --- State ---
    let state = null;
    let soundEnabled = false;
    let wakeLock = null;
    let audioCtx = null;
    let eventSource = null;
    let historyScores = [];
    let lastInstruction = null;
    let measureAnimFrame = null;
    let mockMode = false;

    // --- DOM refs ---
    const $ = (sel) => document.querySelector(sel);
    const $$ = (sel) => document.querySelectorAll(sel);

    // --- Init ---
    document.addEventListener('DOMContentLoaded', init);

    function init() {
        fetchState();
        connectSSE();
        bindEvents();
        requestWakeLock();
    }

    // --- Fetch initial state ---
    async function fetchState() {
        try {
            const resp = await fetch('/api/state');
            const data = await resp.json();
            mockMode = data.mock_mode;
            if (mockMode) {
                $('#mock-controls').classList.remove('hidden');
            }
            if (data.router_status === 'connected') {
                setConnected(true);
            } else {
                setConnected(false, data.router_status);
            }
            if (data.sample) {
                updateScore(data.sample, data.score, data.quality);
            }
            if (data.session) {
                updateSession(data.session);
            }
        } catch (e) {
            console.error('Failed to fetch initial state:', e);
        }
    }

    // --- SSE ---
    function connectSSE() {
        if (eventSource) {
            eventSource.close();
        }
        eventSource = new EventSource('/events');

        eventSource.addEventListener('sample', function (e) {
            try {
                const data = JSON.parse(e.data);
                setConnected(true);
                updateScore(data.sample, data.score, data.quality);
            } catch (err) {
                console.error('sample parse error:', err);
            }
        });

        eventSource.addEventListener('session', function (e) {
            try {
                const data = JSON.parse(e.data);
                updateSession(data);
            } catch (err) {
                console.error('session parse error:', err);
            }
        });

        eventSource.addEventListener('error', function (e) {
            try {
                const data = JSON.parse(e.data);
                setConnected(false, data.error);
            } catch (err) {
                // SSE connection error
            }
        });

        eventSource.onerror = function () {
            setConnected(false, 'Reconnecting…');
        };

        eventSource.onopen = function () {
            setConnected(true);
        };
    }

    // --- Connection status ---
    function setConnected(connected, errorText) {
        const dot = $('#status-dot');
        const text = $('#status-text');

        if (connected) {
            dot.className = 'status-dot connected';
            text.textContent = 'Connected';
            $('#offline-banner').classList.add('hidden');
        } else {
            dot.className = 'status-dot disconnected';
            text.textContent = errorText || 'Disconnected';
            if (errorText && errorText !== 'Reconnecting…') {
                $('#offline-banner').classList.remove('hidden');
                $('#offline-text').textContent = errorText;
            }
        }
    }

    // --- Score update ---
    function updateScore(sample, score, quality) {
        if (!sample) return;

        const scoreEl = $('#big-score');
        const rounded = Math.round(score);
        scoreEl.textContent = rounded;

        // Color class
        scoreEl.className = '';
        if (quality === 'Excellent' || quality === 'Good') {
            scoreEl.className = 'score-excellent';
        } else if (quality === 'Fair') {
            scoreEl.className = 'score-fair';
        } else if (quality === 'Poor') {
            scoreEl.className = 'score-poor';
        } else {
            scoreEl.className = 'score-unknown';
        }

        $('#quality-label').textContent = quality || '--';

        // Metrics
        $('#rsrp-value').textContent = sample.rsrp != null ? Math.round(sample.rsrp) : '--';
        $('#sinr-value').textContent = sample.sinr != null ? Math.round(sample.sinr) : '--';
        $('#rsrq-value').textContent = sample.rsrq != null ? Math.round(sample.rsrq) : '--';

        // History
        historyScores.push(score);
        if (historyScores.length > 300) {
            historyScores.shift();
        }
        drawHistoryChart();
    }

    // --- Session update ---
    function updateSession(s) {
        state = s;

        // Mode tabs
        $$('.tab').forEach(function (tab) {
            tab.classList.toggle('active', tab.dataset.mode === s.mode);
        });

        const isGuided = s.mode === 'guided';
        const isLive = s.mode === 'live';
        const isSpeed = s.mode === 'speed';

        // Show/hide panels
        $('#direction-panel').classList.toggle('hidden', !isGuided);
        $('#live-panel').classList.toggle('hidden', !isLive);
        $('#speed-panel').classList.toggle('hidden', !isSpeed);
        $('#phase-section').classList.toggle('hidden', !isGuided);

        if (isGuided) {
            updateDirectionPanel(s);
            updatePhaseProgress(s);
        }

        if (isLive) {
            updateLivePanel(s);
        }

        updatePositionsTable(s);
        updateMeasureState(s);

        // Tower warning
        if (s.positions && s.positions.length > 0) {
            const lastPos = s.positions[s.positions.length - 1];
            $('#tower-warning').classList.toggle('hidden', !lastPos.tower_switch);
        } else {
            $('#tower-warning').classList.add('hidden');
        }

        // Draw polar chart if we have coarse positions
        if (s.positions) {
            const coarse = s.positions.filter(function (p) { return p.phase === 'COARSE'; });
            if (coarse.length > 0) {
                drawPolarChart(coarse, s.best_position);
            }
        }

        // Measure button state
        const btn = $('#measure-btn');
        if (s.mode === 'guided' && s.phase !== 'DONE' && s.phase !== 'IDLE') {
            btn.disabled = s.measure_state !== 'IDLE';
        } else {
            btn.disabled = true;
        }
    }

    // --- Direction panel ---
    function updateDirectionPanel(s) {
        const inst = s.instruction;
        if (!inst) return;

        const arrow = $('#direction-arrow');
        const text = $('#direction-text');
        const amount = $('#direction-amount');

        // Arrow mapping
        const arrowMap = {
            'TURN_LEFT': '◀',
            'TURN_RIGHT': '▶',
            'TILT_UP': '▲',
            'TILT_DOWN': '▼',
            'HOLD': '✋',
            'DONE': '✔',
            'MEASURE': '📏'
        };

        const newArrow = arrowMap[inst.action] || '🔧';
        arrow.textContent = newArrow;

        // Color
        if (inst.action === 'DONE' || inst.action === 'HOLD') {
            arrow.style.color = 'var(--good)';
        } else {
            arrow.style.color = 'var(--accent)';
        }

        text.textContent = inst.text || '';
        amount.textContent = inst.amount_deg ? 'about ' + inst.amount_deg + '°' : '';

        // Pulse animation on new instruction
        if (lastInstruction && lastInstruction.action !== inst.action) {
            arrow.classList.remove('pulse');
            void arrow.offsetWidth; // trigger reflow
            arrow.classList.add('pulse');

            if (soundEnabled) {
                playBeep();
                if (navigator.vibrate) {
                    navigator.vibrate(100);
                }
            }
        }
        lastInstruction = inst;
    }

    // --- Live panel ---
    function updateLivePanel(s) {
        const trendMap = {
            'WARMER': { arrow: '↗', text: 'Warmer!', color: 'var(--good)' },
            'COLDER': { arrow: '↘', text: 'Colder', color: 'var(--bad)' },
            'STEADY': { arrow: '→', text: 'Steady', color: 'var(--muted)' }
        };

        const trend = trendMap[s.live_trend] || trendMap['STEADY'];
        $('#live-trend-arrow').textContent = trend.arrow;
        $('#live-trend-arrow').style.color = trend.color;
        $('#live-trend-text').textContent = trend.text;
        $('#live-best').textContent = 'Best: ' + (s.live_best_score > 0 ? s.live_best_score.toFixed(1) : '--');
    }

    // --- Phase progress ---
    function updatePhaseProgress(s) {
        const inst = s.instruction;
        if (!inst || !inst.progress) return;

        const phaseNames = {
            'COARSE': 'Coarse Scan',
            'FINE_AZ_10': 'Fine Azimuth 10°',
            'FINE_AZ_5': 'Fine Azimuth 5°',
            'TILT': 'Tilt Adjustment',
            'RECHECK': 'Final Check',
            'DONE': 'Complete',
            'IDLE': 'Idle'
        };

        const name = phaseNames[s.phase] || s.phase;
        const cur = inst.progress.current || 0;
        const total = inst.progress.estimated_total || 1;
        $('#phase-text').textContent = 'Phase: ' + name + '  ' + cur + ' / ~' + total;

        const pct = Math.min(100, (cur / total) * 100);
        $('#phase-progress').style.width = pct + '%';
    }

    // --- Measure state (settle/collect progress) ---
    function updateMeasureState(s) {
        const prog = $('#measure-progress');
        const btn = $('#measure-btn');

        if (s.measure_state === 'IDLE') {
            prog.classList.add('hidden');
            btn.textContent = 'Measure now';
            if (measureAnimFrame) {
                cancelAnimationFrame(measureAnimFrame);
                measureAnimFrame = null;
            }
            return;
        }

        prog.classList.remove('hidden');
        btn.disabled = true;

        // Start animation loop
        if (!measureAnimFrame) {
            animateMeasureProgress(s);
        }
    }

    function animateMeasureProgress(s) {
        if (!state || state.measure_state === 'IDLE') {
            measureAnimFrame = null;
            return;
        }

        const now = Date.now();
        const startMs = new Date(state.measure_start).getTime();
        const elapsed = (now - startMs) / 1000;

        const circumference = 2 * Math.PI * 54;
        const circle = $('#progress-circle');
        const progText = $('#progress-text');
        const btn = $('#measure-btn');

        circle.style.strokeDasharray = circumference;

        if (state.measure_state === 'SETTLING') {
            const total = state.settle_seconds;
            const remaining = Math.max(0, total - elapsed);
            const pct = Math.min(1, elapsed / total);
            circle.style.strokeDashoffset = circumference * (1 - pct);
            circle.style.stroke = 'var(--warn)';
            progText.textContent = 'Settling… ' + Math.ceil(remaining) + 's';
            btn.textContent = 'Settling… ' + Math.ceil(remaining) + 's';
        } else if (state.measure_state === 'COLLECTING') {
            const settleTime = state.settle_seconds;
            const collectElapsed = elapsed - settleTime;
            const total = state.measure_seconds;
            const pct = Math.min(1, collectElapsed / total);
            circle.style.strokeDashoffset = circumference * (1 - pct);
            circle.style.stroke = 'var(--accent)';
            progText.textContent = 'Measuring… ' + Math.ceil(collectElapsed) + 's / ' + total + 's';
            btn.textContent = 'Measuring… ' + Math.ceil(collectElapsed) + 's';
        }

        measureAnimFrame = requestAnimationFrame(function () {
            animateMeasureProgress(s);
        });
    }

    // --- Positions table ---
    function updatePositionsTable(s) {
        const tbody = $('#positions-body');
        if (!s.positions || s.positions.length === 0) {
            tbody.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--muted)">No measurements yet</td></tr>';
            return;
        }

        const bestId = s.best_position ? s.best_position.id : -1;
        const stepSize = s.step_size || 30;

        let html = '';
        for (let i = 0; i < s.positions.length; i++) {
            const p = s.positions[i];
            const isBest = p.id === bestId;
            const azDeg = p.azimuth_step * getStepDeg(p.phase);
            const tiltDeg = p.tilt_step * 5;

            html += '<tr class="' + (isBest ? 'best-row' : '') + '">';
            html += '<td>' + p.id + '</td>';
            html += '<td>' + azDeg + '°</td>';
            html += '<td>' + tiltDeg + '°</td>';
            html += '<td>' + p.avg_score.toFixed(1) + '</td>';
            html += '<td>' + fmtPtr(p.avg_rsrp) + '</td>';
            html += '<td>' + fmtPtr(p.avg_sinr) + '</td>';
            html += '<td>' + (p.tower_switch ? '⚠' : '') + '</td>';
            html += '</tr>';
        }
        tbody.innerHTML = html;
    }

    function getStepDeg(phase) {
        switch (phase) {
            case 'COARSE': return 30;
            case 'FINE_AZ_10': return 10;
            case 'FINE_AZ_5': return 5;
            case 'TILT': return 5;
            case 'RECHECK': return 5;
            default: return 1;
        }
    }

    function fmtPtr(v) {
        return v != null ? Math.round(v) : '--';
    }

    // --- Polar chart ---
    function drawPolarChart(positions, bestPos) {
        const svg = $('#polar-chart');
        const cx = 150, cy = 150, maxR = 120;

        let html = '';

        // Background circle
        html += '<circle cx="' + cx + '" cy="' + cy + '" r="' + maxR + '" fill="none" stroke="rgba(128,128,128,0.2)" stroke-width="1"/>';
        html += '<circle cx="' + cx + '" cy="' + cy + '" r="' + (maxR * 0.5) + '" fill="none" stroke="rgba(128,128,128,0.1)" stroke-width="1"/>';

        // 12 spoke guidelines
        for (let i = 0; i < 12; i++) {
            const angle = (i * 30 - 90) * Math.PI / 180;
            const x2 = cx + maxR * Math.cos(angle);
            const y2 = cy + maxR * Math.sin(angle);
            html += '<line x1="' + cx + '" y1="' + cy + '" x2="' + x2 + '" y2="' + y2 + '" stroke="rgba(128,128,128,0.15)" stroke-width="1"/>';
        }

        // Labels
        const labels = ['0°', '', '', '90°', '', '', '180°', '', '', '270°', '', ''];
        for (let i = 0; i < 12; i++) {
            if (!labels[i]) continue;
            const angle = (i * 30 - 90) * Math.PI / 180;
            const lx = cx + (maxR + 15) * Math.cos(angle);
            const ly = cy + (maxR + 15) * Math.sin(angle);
            html += '<text x="' + lx + '" y="' + ly + '" text-anchor="middle" dominant-baseline="middle" fill="var(--muted)" font-size="11">' + labels[i] + '</text>';
        }

        // Score spokes
        const bestId = bestPos ? bestPos.id : -1;
        for (let i = 0; i < positions.length; i++) {
            const p = positions[i];
            const angle = (i * 30 - 90) * Math.PI / 180;
            const r = (p.avg_score / 100) * maxR;
            const x2 = cx + r * Math.cos(angle);
            const y2 = cy + r * Math.sin(angle);

            const isBest = p.id === bestId;
            const color = isBest ? 'var(--accent)' : getScoreColor(p.avg_score, p.avg_rsrp);
            const width = isBest ? 4 : 3;

            html += '<line x1="' + cx + '" y1="' + cy + '" x2="' + x2 + '" y2="' + y2 + '" stroke="' + color + '" stroke-width="' + width + '" stroke-linecap="round"/>';
            html += '<circle cx="' + x2 + '" cy="' + y2 + '" r="' + (isBest ? 6 : 4) + '" fill="' + color + '"/>';

            // Score label
            const lx = cx + (r + 12) * Math.cos(angle);
            const ly = cy + (r + 12) * Math.sin(angle);
            html += '<text x="' + lx + '" y="' + ly + '" text-anchor="middle" dominant-baseline="middle" fill="var(--text)" font-size="10" font-weight="600">' + Math.round(p.avg_score) + '</text>';
        }

        svg.innerHTML = html;
    }

    function getScoreColor(score, rsrp) {
        if (rsrp != null) {
            if (rsrp > -90) return 'var(--good)';
            if (rsrp > -100) return 'var(--good)';
            if (rsrp > -110) return 'var(--warn)';
            return 'var(--bad)';
        }
        if (score > 70) return 'var(--good)';
        if (score > 40) return 'var(--warn)';
        return 'var(--bad)';
    }

    // --- History chart ---
    function drawHistoryChart() {
        const svg = $('#history-chart');
        const w = 500, h = 150;
        const pad = 5;

        let html = '';

        // Grid lines
        for (let y = 0; y <= 100; y += 25) {
            const yPos = h - pad - ((y / 100) * (h - 2 * pad));
            html += '<line x1="0" y1="' + yPos + '" x2="' + w + '" y2="' + yPos + '" stroke="rgba(128,128,128,0.15)" stroke-width="1"/>';
            html += '<text x="2" y="' + (yPos - 3) + '" fill="var(--muted)" font-size="9">' + y + '</text>';
        }

        if (historyScores.length < 2) {
            svg.innerHTML = html;
            return;
        }

        // Polyline
        const points = [];
        const step = (w - 2 * pad) / (historyScores.length - 1);
        for (let i = 0; i < historyScores.length; i++) {
            const x = pad + i * step;
            const y = h - pad - ((historyScores[i] / 100) * (h - 2 * pad));
            points.push(x.toFixed(1) + ',' + y.toFixed(1));
        }

        html += '<polyline points="' + points.join(' ') + '" fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linejoin="round"/>';

        svg.innerHTML = html;
    }

    // --- Event binding ---
    function bindEvents() {
        // Mode tabs
        $$('.tab').forEach(function (tab) {
            tab.addEventListener('click', function () {
                const mode = this.dataset.mode;
                postJSON('/api/session/start', { mode: mode });
            });
        });

        // Measure
        $('#measure-btn').addEventListener('click', function () {
            postJSON('/api/session/measure', {});
        });

        // Re-measure
        $('#remeasure-btn').addEventListener('click', function () {
            postJSON('/api/session/remeasure', {});
        });

        // Skip
        $('#skip-btn').addEventListener('click', function () {
            postJSON('/api/session/skip', {});
        });

        // Mark (live mode)
        $('#mark-btn').addEventListener('click', function () {
            postJSON('/api/session/mark', {});
        });

        // Reset
        $('#reset-btn').addEventListener('click', function () {
            if (confirm('Reset all measurements?')) {
                postJSON('/api/session/reset', {});
                historyScores = [];
                drawHistoryChart();
            }
        });

        // Speed Test
        let speedInterval = null;
        $('#speedtest-btn').addEventListener('click', async function () {
            const btn = this;
            btn.disabled = true;
            $('#speed-status-text').textContent = 'Starting speed test...';
            $('#speed-ping').textContent = '--';
            $('#speed-download').textContent = '--';
            $('#speed-upload').textContent = '--';

            try {
                const startResp = await fetch('/api/speedtest/start', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: '{}'
                });
                const startData = await startResp.json();
                if (!startData.ok && startData.error !== 'speed test already running') {
                    $('#speed-status-text').textContent = 'Error: ' + (startData.error || 'Failed to start');
                    btn.disabled = false;
                    return;
                }
            } catch (err) {
                $('#speed-status-text').textContent = 'Network error starting test';
                btn.disabled = false;
                return;
            }

            if (speedInterval) clearInterval(speedInterval);
            speedInterval = setInterval(async function () {
                try {
                    const resp = await fetch('/api/speedtest/status');
                    const data = await resp.json();
                    
                    if (data.ping_ms > 0) {
                        $('#speed-ping').textContent = Math.round(data.ping_ms);
                    }
                    if (data.download_mbps > 0) {
                        $('#speed-download').textContent = data.download_mbps.toFixed(1);
                    }
                    if (data.upload_mbps > 0) {
                        $('#speed-upload').textContent = data.upload_mbps.toFixed(1);
                    }

                    if (data.stage === 'latency') {
                        $('#speed-status-text').textContent = 'Testing Latency / Ping...';
                    } else if (data.stage === 'download') {
                        $('#speed-status-text').textContent = 'Testing Download Speed...';
                    } else if (data.stage === 'upload') {
                        $('#speed-status-text').textContent = 'Testing Upload Speed...';
                    } else if (data.stage === 'done') {
                        $('#speed-status-text').textContent = 'Completed!';
                        btn.disabled = false;
                        clearInterval(speedInterval);
                    } else if (data.stage === 'error') {
                        $('#speed-status-text').textContent = 'Error: ' + (data.error || 'Failed');
                        btn.disabled = false;
                        clearInterval(speedInterval);
                    }
                } catch (e) {
                    // Ignore transient fetch errors
                }
            }, 500);
        });

        // Sound toggle
        $('#sound-toggle').addEventListener('click', function () {
            soundEnabled = !soundEnabled;
            this.textContent = soundEnabled ? '🔊' : '🔇';
        });

        // Mock controls
        $$('[data-move]').forEach(function (btn) {
            btn.addEventListener('click', function () {
                postJSON('/api/mock/move', { direction: this.dataset.move });
            });
        });
    }

    // --- API helpers ---
    async function postJSON(url, body) {
        try {
            const resp = await fetch(url, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });
            const data = await resp.json();
            if (!data.ok) {
                console.error('API error:', data.error);
            }
        } catch (e) {
            console.error('Request failed:', e);
        }
    }

    // --- Sound ---
    function playBeep() {
        try {
            if (!audioCtx) {
                audioCtx = new (window.AudioContext || window.webkitAudioContext)();
            }
            const osc = audioCtx.createOscillator();
            const gain = audioCtx.createGain();
            osc.connect(gain);
            gain.connect(audioCtx.destination);
            osc.frequency.value = 880;
            gain.gain.value = 0.3;
            osc.start();
            osc.stop(audioCtx.currentTime + 0.15);
        } catch (e) {
            // Audio not available
        }
    }

    // --- Wake Lock ---
    async function requestWakeLock() {
        if ('wakeLock' in navigator) {
            try {
                wakeLock = await navigator.wakeLock.request('screen');
                wakeLock.addEventListener('release', function () {
                    // Re-request on visibility change
                    document.addEventListener('visibilitychange', function () {
                        if (document.visibilityState === 'visible') {
                            requestWakeLock();
                        }
                    }, { once: true });
                });
            } catch (e) {
                // Wake lock not available
            }
        }
    }

})();
