// Antenna Aligner - Frontend Application
// Vanilla JS, no frameworks, no build step.

(function () {
    'use strict';

    // --- State ---
    let state = null;
    let soundEnabled = localStorage.getItem('antenna_sound') === 'true';
    let vibeEnabled = localStorage.getItem('antenna_vibrate') !== null
        ? localStorage.getItem('antenna_vibrate') === 'true'
        : true;
    let wakeLock = null;
    let audioCtx = null;
    let eventSource = null;
    let historyScores = [];
    let lastInstruction = null;
    let lastInstructionKey = null;
    let lastMeasureState = 'IDLE';
    let lastWarnedTowerPosId = null;
    let lastLiveTrend = 'STEADY';
    let lastLiveBestScore = 0;
    let instructionFiredThisTick = false;
    let measureAnimFrame = null;
    let mockMode = false;

    // Tower state
    let towersData = null;
    let activeTowerView = 'radial';
    let selectedTower = null;
    let leafletMap = null;
    let leafletMarkers = [];
    let leafletMapHasFitBounds = false;
    let lastFittedUserLat = null;
    let lastFittedUserLon = null;
    let lastFittedServingId = null;
    let consecutiveTileErrors = 0;
    let totalTilesLoaded = 0;
    let currentTab = 'guided';
    let showAllOperators = false;
    let gpsWatchId = null;
    let gpsTimeoutId = null;
    let bestGpsFix = null;
    let pendingLowAccFix = null;

    // --- DOM refs ---
    const $ = (sel) => document.querySelector(sel);
    const $$ = (sel) => document.querySelectorAll(sel);

    // --- Init ---
    document.addEventListener('DOMContentLoaded', init);

    function init() {
        updateSoundUI();
        updateVibeUI();
        fetchState();
        fetchTowers();
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
            if (data.towers) {
                updateTowersUI(data.towers);
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

        eventSource.addEventListener('towers', function (e) {
            try {
                const data = JSON.parse(e.data);
                updateTowersUI(data);
            } catch (err) {
                console.error('towers parse error:', err);
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
        instructionFiredThisTick = false;

        const isGuided = s.mode === 'guided';
        const isLive = s.mode === 'live';
        const isSpeed = s.mode === 'speed';

        if (currentTab !== 'towers') {
            // Mode tabs
            $$('.tab').forEach(function (tab) {
                tab.classList.toggle('active', tab.dataset.mode === s.mode);
            });

            // Show/hide panels
            $('#direction-panel').classList.toggle('hidden', !isGuided);
            $('#live-panel').classList.toggle('hidden', !isLive);
            $('#speed-panel').classList.toggle('hidden', !isSpeed);
            $('#phase-section').classList.toggle('hidden', !isGuided);
            $('#towers-panel').classList.add('hidden');
        } else {
            $$('.tab').forEach(function (tab) {
                tab.classList.toggle('active', tab.dataset.mode === 'towers');
            });
            $('#direction-panel').classList.add('hidden');
            $('#live-panel').classList.add('hidden');
            $('#speed-panel').classList.add('hidden');
            $('#phase-section').classList.add('hidden');
            $('#towers-panel').classList.remove('hidden');
        }

        // Serving tower hint banner in guided mode
        if (isGuided && s.tower_hint) {
            $('#guided-tower-hint').classList.remove('hidden');
            $('#guided-tower-hint-text').textContent = s.tower_hint;
        } else {
            $('#guided-tower-hint').classList.add('hidden');
        }

        // Low score offer full scan box
        if (isGuided && s.offer_full_scan) {
            $('#offer-full-scan-box').classList.remove('hidden');
        } else {
            $('#offer-full-scan-box').classList.add('hidden');
        }

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
            if (lastPos.tower_switch && lastPos.id !== lastWarnedTowerPosId) {
                lastWarnedTowerPosId = lastPos.id;
                playWarningSound();
                vibrateWarning();
            }
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

        // Redraw radial heading pointer if radial view is visible
        if (activeTowerView === 'radial') {
            renderRadialTowerView();
        }

        // Measure button state
        const btn = $('#measure-btn');
        if (s.mode === 'guided' && s.phase !== 'DONE' && s.phase !== 'IDLE') {
            btn.disabled = s.measure_state !== 'IDLE';
        } else {
            btn.disabled = true;
        }
    }

    function getInstructionKey(inst) {
        if (!inst) return '';
        const cur = (inst.progress && inst.progress.current != null) ? inst.progress.current : 0;
        return `${inst.phase || ''}:${inst.action || ''}:${inst.amount_deg || 0}:${cur}:${inst.text || ''}`;
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

        const instKey = getInstructionKey(inst);
        const isNew = lastInstructionKey !== null && instKey !== lastInstructionKey;

        // Pulse animation, audio, and vibration on step update
        if (isNew) {
            arrow.classList.remove('pulse');
            void arrow.offsetWidth; // trigger reflow
            arrow.classList.add('pulse');

            instructionFiredThisTick = true;

            if (inst.action === 'DONE') {
                playSuccessSound();
                vibrateSuccess();
            } else {
                playInstructionSound();
                vibrateInstruction();
            }
        }
        lastInstructionKey = instKey;
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

        // Audio & vibration cues in Live mode
        if (s.live_best_score > 0 && s.live_best_score > lastLiveBestScore) {
            if (lastLiveBestScore > 0) {
                playLiveNewBestSound();
                vibrateLiveNewBest();
            }
            lastLiveBestScore = s.live_best_score;
        }

        if (s.live_trend && s.live_trend !== lastLiveTrend) {
            if (s.live_trend === 'WARMER') {
                playLiveWarmerSound();
                vibrateLiveWarmer();
            }
            lastLiveTrend = s.live_trend;
        }
    }

    // --- Phase progress ---
    function updatePhaseProgress(s) {
        const inst = s.instruction;
        if (!inst || !inst.progress) return;

        const phaseNames = {
            'HINT': 'Tower Hint',
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

        const prevMeasureState = lastMeasureState;
        lastMeasureState = s.measure_state;

        if (s.measure_state === 'IDLE') {
            prog.classList.add('hidden');
            btn.textContent = 'Measure now';
            if (measureAnimFrame) {
                cancelAnimationFrame(measureAnimFrame);
                measureAnimFrame = null;
            }

            // If measurement just completed
            if (prevMeasureState === 'COLLECTING' || prevMeasureState === 'SETTLING') {
                if (!instructionFiredThisTick) {
                    playMeasureCompleteSound();
                    vibrateMeasureComplete();
                }
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
            case 'HINT': return 30;
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

    // --- Navigation & Tabs ---
    function setActiveTab(tabName) {
        currentTab = tabName;
        $$('.tab').forEach(function (t) {
            t.classList.toggle('active', t.dataset.mode === tabName);
        });

        $('#towers-panel').classList.toggle('hidden', tabName !== 'towers');
        $('#direction-panel').classList.toggle('hidden', tabName !== 'guided');
        $('#phase-section').classList.toggle('hidden', tabName !== 'guided');
        $('#live-panel').classList.toggle('hidden', tabName !== 'live');
        $('#speed-panel').classList.toggle('hidden', tabName !== 'speed');

        if (tabName === 'towers') {
            if (activeTowerView === 'radial') {
                renderRadialTowerView();
            } else {
                renderLeafletMap();
                if (leafletMap) {
                    setTimeout(function () { leafletMap.invalidateSize(); }, 100);
                }
            }
        } else {
            lastInstructionKey = null;
            lastInstruction = null;
            lastLiveBestScore = 0;
            lastLiveTrend = 'STEADY';
            postJSON('/api/session/start', { mode: tabName });
        }
    }

    // --- Towers & Map Logic ---
    async function fetchTowers(showAll) {
        try {
            const url = showAll ? '/api/towers?all=true' : '/api/towers';
            const resp = await fetch(url);
            const data = await resp.json();
            updateTowersUI(data);
        } catch (e) {
            console.error('Failed to fetch towers:', e);
        }
    }

    async function saveLocation(lat, lon, accuracy) {
        const payload = { lat: lat, lon: lon };
        if (accuracy != null && !isNaN(accuracy)) {
            payload.accuracy = accuracy;
        }
        const res = await postJSON('/api/location', payload);
        if (res && res.ok) {
            const accStr = (accuracy != null && !isNaN(accuracy)) ? ` (±${Math.round(accuracy)}m)` : '';
            $('#loc-status').textContent = `Location saved!${accStr}`;
            const warnEl = $('#loc-accuracy-warning');
            if (warnEl) warnEl.classList.add('hidden');
            fetchTowers(showAllOperators);
        } else {
            $('#loc-status').textContent = 'Failed to save location: ' + ((res && res.error) || 'network error');
        }
    }

    function updateTowersUI(tState) {
        if (!tState) return;
        towersData = tState;

        // User location
        if (tState.user_lat != null && tState.user_lon != null && (tState.user_lat !== 0 || tState.user_lon !== 0)) {
            const latInput = $('#loc-lat');
            const lonInput = $('#loc-lon');
            if (document.activeElement !== latInput) latInput.value = tState.user_lat;
            if (document.activeElement !== lonInput) lonInput.value = tState.user_lon;
            $('#location-empty-msg').classList.add('hidden');
            const accStr = (tState.user_accuracy != null && tState.user_accuracy > 0) ? ` (±${Math.round(tState.user_accuracy)}m)` : '';
            $('#loc-status').textContent = `Location set: ${tState.user_lat.toFixed(5)}, ${tState.user_lon.toFixed(5)}${accStr}`;
        } else {
            $('#location-empty-msg').classList.remove('hidden');
            $('#loc-status').textContent = '';
        }

        // Serving tower card
        const serving = tState.serving;
        if (serving) {
            const distStr = serving.distance_m != null ? ` • ~${formatDist(serving.distance_m)}` : '';
            const bearStr = serving.bearing_deg != null ? ` at ${Math.round(serving.bearing_deg)}° (${serving.compass_text || ''})` : '';
            const bandStr = (serving.cells && serving.cells[0] && serving.cells[0].band) ? ` [${serving.cells[0].band}]` : '';
            $('#serving-tower-summary').textContent = `eNB ${serving.enb}${distStr}${bearStr}${bandStr}`;

            if (serving.accuracy > 0) {
                $('#badge-accuracy').classList.remove('hidden');
                $('#badge-accuracy').textContent = `±${serving.accuracy}m`;
            } else {
                $('#badge-accuracy').classList.add('hidden');
            }

            $('#badge-source').textContent = serving.source || 'manual';
            $('#badge-source').classList.remove('hidden');

            if (tState.offline) {
                $('#badge-offline').classList.remove('hidden');
            } else {
                $('#badge-offline').classList.add('hidden');
            }

            $('#tower-not-found-banner').classList.add('hidden');
        } else {
            const curENB = (tState.serving_enb > 0) ? tState.serving_enb : (state && state.sample && state.sample.enb ? state.sample.enb : null);
            const curCellID = tState.serving_cell_id || (state && state.sample && state.sample.cell_id ? state.sample.cell_id : '');

            if (curENB || curCellID) {
                const plmnStr = (state && state.sample && state.sample.plmn) ? ` • PLMN ${state.sample.plmn}` : '';
                $('#serving-tower-summary').textContent = `eNB ${curENB || '--'} (Cell: ${curCellID})${plmnStr} • GPS Unknown`;
                $('#tower-not-found-banner').classList.remove('hidden');
                $('#tower-not-found-banner').innerHTML = `
                    <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px;">
                        <span>⚠️ Serving tower eNB <strong>${curENB || '--'}</strong> position not in database.</span>
                        <button id="btn-quick-add-serving" class="primary-btn btn-sm">➕ Set Location for eNB ${curENB || ''}</button>
                    </div>
                `;
                const btnQuick = $('#btn-quick-add-serving');
                if (btnQuick) {
                    btnQuick.onclick = function() {
                        openManualTowerModal(curENB);
                    };
                }
            } else {
                $('#serving-tower-summary').textContent = 'No serving tower resolved';
                $('#tower-not-found-banner').classList.add('hidden');
            }

            $('#badge-accuracy').classList.add('hidden');
            $('#badge-source').classList.add('hidden');
            $('#badge-offline').classList.add('hidden');
        }

        // Render current view
        if (activeTowerView === 'radial') {
            renderRadialTowerView();
        } else {
            renderLeafletMap();
        }

        // Update nearby table
        updateNearbyTable(tState.nearby || []);
    }

    function updateNearbyTable(nearby) {
        const countEl = $('#nearby-count');
        const tbody = $('#nearby-body');
        if (countEl) countEl.textContent = nearby.length;

        if (!nearby || nearby.length === 0) {
            tbody.innerHTML = '<tr><td colspan="4" class="muted center">No towers in radius</td></tr>';
            return;
        }

        let html = '';
        for (let i = 0; i < nearby.length; i++) {
            const tw = nearby[i];
            const isServing = tw.is_serving;
            const star = isServing ? ' ⭐' : '';
            html += `<tr data-tower-id="${tw.id || tw.enb}" style="cursor: pointer;">`;
            html += `<td><strong>${tw.enb}${star}</strong></td>`;
            html += `<td>${formatDist(tw.distance_m)}</td>`;
            html += `<td>${Math.round(tw.bearing_deg)}° ${tw.compass_text || ''}</td>`;
            html += `<td style="text-transform: capitalize;">${tw.source || 'manual'}</td>`;
            html += `</tr>`;
        }
        tbody.innerHTML = html;
    }

    function formatDist(meters) {
        if (meters == null || isNaN(meters)) return '--';
        if (meters < 1000) return Math.round(meters) + ' m';
        return (meters / 1000).toFixed(1) + ' km';
    }

    function distToRadius(m) {
        if (m <= 0) return 0;
        if (m <= 1000) return (m / 1000) * 30;
        if (m <= 2000) return 30 + ((m - 1000) / 1000) * 30;
        if (m <= 5000) return 60 + ((m - 2000) / 3000) * 35;
        if (m <= 10000) return 95 + ((m - 5000) / 5000) * 35;
        return 130; // clamped to outer rim
    }

    function renderRadialTowerView() {
        const svg = $('#radial-svg');
        if (!svg) return;

        const cx = 160, cy = 160;
        const maxR = 130;

        let html = '';

        // Rings: 1km (30px), 2km (60px), 5km (95px), 10km (130px)
        const rings = [
            { r: 30, label: '1k' },
            { r: 60, label: '2k' },
            { r: 95, label: '5k' },
            { r: 130, label: '10k' }
        ];

        for (const ring of rings) {
            html += `<circle cx="${cx}" cy="${cy}" r="${ring.r}" fill="none" stroke="rgba(128,128,128,0.2)" stroke-width="1"/>`;
            const lblX = cx + ring.r * 0.707 + 2;
            const lblY = cy - ring.r * 0.707 - 2;
            html += `<text x="${lblX}" y="${lblY}" fill="var(--muted)" font-size="9" opacity="0.7">${ring.label}</text>`;
        }

        // Crosshairs (N-S, E-W)
        html += `<line x1="${cx}" y1="${cy - maxR}" x2="${cx}" y2="${cy + maxR}" stroke="rgba(128,128,128,0.15)" stroke-width="1"/>`;
        html += `<line x1="${cx - maxR}" y1="${cy}" x2="${cx + maxR}" y2="${cy}" stroke="rgba(128,128,128,0.15)" stroke-width="1"/>`;

        // Compass cardinal labels
        html += `<text x="${cx}" y="${cy - maxR - 8}" text-anchor="middle" dominant-baseline="middle" fill="var(--accent)" font-weight="700" font-size="12">N</text>`;
        html += `<text x="${cx + maxR + 12}" y="${cy}" text-anchor="start" dominant-baseline="middle" fill="var(--muted)" font-size="11">E</text>`;
        html += `<text x="${cx}" y="${cy + maxR + 12}" text-anchor="middle" dominant-baseline="middle" fill="var(--muted)" font-size="11">S</text>`;
        html += `<text x="${cx - maxR - 12}" y="${cy}" text-anchor="end" dominant-baseline="middle" fill="var(--muted)" font-size="11">W</text>`;

        // Center user marker
        html += `<circle cx="${cx}" cy="${cy}" r="5" fill="var(--good)"/>`;
        html += `<text x="${cx}" y="${cy + 14}" text-anchor="middle" fill="var(--muted)" font-size="9">You</text>`;

        // Antenna heading pointer from guided session best position (labeled relative)
        if (state && state.best_position) {
            const stepDeg = getStepDeg(state.best_position.phase);
            const headingDeg = (state.best_position.azimuth_step * stepDeg) % 360;
            const hRad = (headingDeg * Math.PI) / 180;
            const hX = cx + (maxR * 0.85) * Math.sin(hRad);
            const hY = cy - (maxR * 0.85) * Math.cos(hRad);
            html += `<line x1="${cx}" y1="${cy}" x2="${hX}" y2="${hY}" stroke="var(--warn)" stroke-width="2" stroke-dasharray="4,2"/>`;
            html += `<polygon points="${hX},${hY} ${hX - 4 * Math.cos(hRad)},${hY - 4 * Math.sin(hRad)} ${hX + 4 * Math.cos(hRad)},${hY + 4 * Math.sin(hRad)}" fill="var(--warn)"/>`;
            html += `<text x="${hX}" y="${hY < cy ? hY - 6 : hY + 12}" text-anchor="middle" fill="var(--warn)" font-size="8">Best (Relative)</text>`;
        }

        // Draw nearby towers (hollow circles)
        if (towersData && towersData.nearby) {
            for (const tw of towersData.nearby) {
                if (tw.is_serving) continue;
                const rad = (tw.bearing_deg * Math.PI) / 180;
                const r = distToRadius(tw.distance_m);
                const x = cx + r * Math.sin(rad);
                const y = cy - r * Math.cos(rad);

                html += `<g class="tower-marker" data-tower-id="${tw.id || tw.enb}" style="cursor: pointer;">`;
                html += `<circle cx="${x}" cy="${y}" r="5" fill="var(--bg, #111)" stroke="var(--text)" stroke-width="1.5"/>`;
                html += `<text x="${x}" y="${y - 8}" text-anchor="middle" fill="var(--muted)" font-size="9">${tw.enb}</text>`;
                html += `</g>`;
            }
        }

        // Draw serving tower (filled dot + line)
        if (towersData && towersData.serving) {
            const srv = towersData.serving;
            const sRad = (srv.bearing_deg * Math.PI) / 180;
            const sR = distToRadius(srv.distance_m);
            const sX = cx + sR * Math.sin(sRad);
            const sY = cy - sR * Math.cos(sRad);

            // Line from user to serving tower
            html += `<line x1="${cx}" y1="${cy}" x2="${sX}" y2="${sY}" stroke="var(--accent)" stroke-width="2" stroke-dasharray="3,3"/>`;

            // Serving tower dot
            html += `<g class="tower-marker serving-marker" data-tower-id="${srv.id || srv.enb}" style="cursor: pointer;">`;
            html += `<circle cx="${sX}" cy="${sY}" r="7" fill="var(--accent)" stroke="#ffffff" stroke-width="2"/>`;
            const band = (srv.cells && srv.cells[0] && srv.cells[0].band) ? ` (${srv.cells[0].band})` : '';
            html += `<text x="${sX}" y="${sY - 10}" text-anchor="middle" fill="var(--accent)" font-weight="700" font-size="10">eNB ${srv.enb}${band}</text>`;
            html += `<text x="${sX}" y="${sY + 16}" text-anchor="middle" fill="var(--text)" font-size="9">${formatDist(srv.distance_m)}</text>`;
            html += `</g>`;
        }

        svg.innerHTML = html;
    }

    function renderLeafletMap(forceRecenter) {
        if (typeof L === 'undefined') return;

        const container = $('#map-container');
        if (!container || container.classList.contains('hidden')) return;

        const hasUserLoc = towersData && towersData.user_lat != null && towersData.user_lon != null && (towersData.user_lat !== 0 || towersData.user_lon !== 0);
        const centerLat = hasUserLoc ? towersData.user_lat : 32.8908;
        const centerLon = hasUserLoc ? towersData.user_lon : 13.1796;

        if (!leafletMap) {
            leafletMap = L.map('leaflet-map').setView([centerLat, centerLon], hasUserLoc ? 14 : 5);
            const tileLayer = L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
                maxZoom: 19,
                attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
            }).addTo(leafletMap);

            tileLayer.on('tileload', function () {
                consecutiveTileErrors = 0;
                totalTilesLoaded++;
            });

            tileLayer.on('tileerror', function () {
                consecutiveTileErrors++;
                if (consecutiveTileErrors >= 6 && totalTilesLoaded === 0) {
                    const notice = $('#map-fallback-notice');
                    if (notice) {
                        notice.classList.remove('hidden');
                        setTimeout(function () {
                            notice.classList.add('hidden');
                            switchTowerView('radial');
                        }, 3000);
                    }
                }
            });

            leafletMap.on('click', function (e) {
                const lat = e.latlng.lat;
                const lon = e.latlng.lng;
                $('#loc-lat').value = lat.toFixed(6);
                $('#loc-lon').value = lon.toFixed(6);
                saveLocation(lat, lon, null);
            });
        }

        leafletMap.invalidateSize();

        // Clear previous markers
        for (const m of leafletMarkers) {
            m.remove();
        }
        leafletMarkers = [];

        const bounds = [];

        // User position marker
        if (towersData && towersData.user_lat != null && towersData.user_lon != null && (towersData.user_lat !== 0 || towersData.user_lon !== 0)) {
            const accText = (towersData.user_accuracy != null && towersData.user_accuracy > 0) ? ` ±${Math.round(towersData.user_accuracy)}m` : '';
            const userMarker = L.circleMarker([towersData.user_lat, towersData.user_lon], {
                radius: 7,
                fillColor: '#22c55e',
                color: '#ffffff',
                weight: 2,
                opacity: 1,
                fillOpacity: 1
            }).addTo(leafletMap).bindTooltip(`Your Antenna (${towersData.user_lat.toFixed(5)}, ${towersData.user_lon.toFixed(5)}${accText})`);
            leafletMarkers.push(userMarker);
            bounds.push([towersData.user_lat, towersData.user_lon]);

            // Accuracy circle if accuracy is known
            if (towersData.user_accuracy != null && towersData.user_accuracy > 0) {
                const accCircle = L.circle([towersData.user_lat, towersData.user_lon], {
                    radius: towersData.user_accuracy,
                    color: '#22c55e',
                    fillColor: '#22c55e',
                    fillOpacity: 0.12,
                    weight: 1,
                    dashArray: '3, 3'
                }).addTo(leafletMap);
                leafletMarkers.push(accCircle);
            }
        }

        // Nearby towers
        if (towersData && towersData.nearby) {
            for (const tw of towersData.nearby) {
                if (tw.is_serving) continue;
                const m = L.circleMarker([tw.lat, tw.lon], {
                    radius: 6,
                    fillColor: '#1e293b',
                    color: '#94a3b8',
                    weight: 2,
                    opacity: 1,
                    fillOpacity: 0.9
                }).addTo(leafletMap).bindTooltip(`eNB ${tw.enb} (~${formatDist(tw.distance_m)})`);
                m.on('click', function () { showTowerDetails(tw); });
                leafletMarkers.push(m);
                // Exclude unreliable towers from bounds
                if (!tw.unreliable && tw.distance_m <= 50000) {
                    bounds.push([tw.lat, tw.lon]);
                }
            }
        }

        // Serving tower
        if (towersData && towersData.serving) {
            const srv = towersData.serving;
            const sm = L.circleMarker([srv.lat, srv.lon], {
                radius: 8,
                fillColor: '#3b82f6',
                color: '#ffffff',
                weight: 2,
                opacity: 1,
                fillOpacity: 1
            }).addTo(leafletMap).bindTooltip(`Serving: eNB ${srv.enb} (~${formatDist(srv.distance_m)})`);
            sm.on('click', function () { showTowerDetails(srv); });
            leafletMarkers.push(sm);
            if (!srv.unreliable && srv.distance_m <= 50000) {
                bounds.push([srv.lat, srv.lon]);
            }

            // Line from user to serving tower
            if (towersData.user_lat != null && (towersData.user_lat !== 0 || towersData.user_lon !== 0)) {
                const line = L.polyline([
                    [towersData.user_lat, towersData.user_lon],
                    [srv.lat, srv.lon]
                ], {
                    color: '#3b82f6',
                    weight: 2,
                    dashArray: '4, 4'
                }).addTo(leafletMap);
                leafletMarkers.push(line);
            }
        }

        const userLocChanged = (towersData && (towersData.user_lat !== lastFittedUserLat || towersData.user_lon !== lastFittedUserLon));
        const currentServingId = (towersData && towersData.serving) ? (towersData.serving.id || towersData.serving.enb) : null;
        const servingChanged = (currentServingId !== lastFittedServingId);
        const shouldFitBounds = forceRecenter || !leafletMapHasFitBounds || userLocChanged || servingChanged;

        if (shouldFitBounds) {
            leafletMapHasFitBounds = true;
            lastFittedUserLat = towersData ? towersData.user_lat : null;
            lastFittedUserLon = towersData ? towersData.user_lon : null;
            lastFittedServingId = currentServingId;

            if (bounds.length > 1) {
                try {
                    leafletMap.fitBounds(bounds, { padding: [30, 30], maxZoom: 15 });
                } catch (e) {}
            } else if (bounds.length === 1) {
                leafletMap.setView(bounds[0], 14);
            }
        }
    }

    function openManualTowerModal(enb) {
        const form = $('#manual-tower-form');
        if (!form) return;
        form.classList.remove('hidden');
        if (enb) {
            $('#man-enb').value = enb;
        } else if (state && state.sample && state.sample.enb > 0) {
            $('#man-enb').value = state.sample.enb;
        }
        if (state && state.sample) {
            if (state.sample.mcc > 0) $('#man-mcc').value = state.sample.mcc;
            if (state.sample.mnc != null) $('#man-mnc').value = state.sample.mnc;
        }
        if (towersData && towersData.user_lat != null && (towersData.user_lat !== 0 || towersData.user_lon !== 0)) {
            if (!$('#man-lat').value) $('#man-lat').value = towersData.user_lat;
            if (!$('#man-lon').value) $('#man-lon').value = towersData.user_lon;
        }
        form.scrollIntoView({ behavior: 'smooth' });
    }

    function switchTowerView(view) {
        activeTowerView = view;
        $('#btn-view-radial').classList.toggle('active', view === 'radial');
        $('#btn-view-map').classList.toggle('active', view === 'map');
        $('#radial-container').classList.toggle('hidden', view !== 'radial');
        $('#map-container').classList.toggle('hidden', view !== 'map');

        if (view === 'radial') {
            renderRadialTowerView();
        } else {
            renderLeafletMap();
            if (leafletMap) {
                setTimeout(function () { leafletMap.invalidateSize(); }, 50);
            }
        }
    }

    function findTowerById(id) {
        if (!towersData) return null;
        if (towersData.serving && (towersData.serving.id === id || String(towersData.serving.enb) === String(id))) {
            return towersData.serving;
        }
        if (towersData.nearby) {
            return towersData.nearby.find(function (t) {
                return t.id === id || String(t.enb) === String(id);
            }) || null;
        }
        return null;
    }

    function showTowerDetails(tw) {
        if (!tw) return;
        selectedTower = tw;
        const card = $('#tower-detail-card');
        if (!card) return;

        $('#td-title').textContent = `eNB ${tw.enb} ${tw.is_serving ? '(Serving)' : ''}`;

        const distStr = formatDist(tw.distance_m);
        const bearStr = `${Math.round(tw.bearing_deg || 0)}° (${tw.compass_text || ''})`;
        const accStr = tw.accuracy > 0 ? `±${tw.accuracy}m` : 'Unknown';
        const srcStr = tw.source || 'manual';
        const coordsStr = `${tw.lat.toFixed(5)}, ${tw.lon.toFixed(5)}`;

        // Approximate Free Space Path Loss estimate (rough sanity check for 1800 MHz)
        let fsplStr = '--';
        if (tw.distance_m > 0) {
            const distKm = tw.distance_m / 1000;
            const fspl = 32.44 + (20 * Math.log10(distKm)) + (20 * Math.log10(1800));
            fsplStr = `~${Math.round(fspl)} dB (1.8GHz ref)`;
        }

        let cellsHtml = '';
        if (tw.cells && tw.cells.length > 0) {
            cellsHtml = '<div style="margin-top:6px;"><strong>Cells / Sectors:</strong><ul style="margin:4px 0;padding-left:18px;">';
            for (const c of tw.cells) {
                cellsHtml += `<li>Sector ${c.sector} ${c.band ? '• Band ' + c.band : ''} ${c.pci ? '• PCI ' + c.pci : ''}</li>`;
            }
            cellsHtml += '</ul></div>';
        }

        const html = `
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 6px; margin-bottom: 8px;">
                <div><span style="color:var(--muted)">Distance:</span> <strong>${distStr}</strong></div>
                <div><span style="color:var(--muted)">Bearing:</span> <strong>${bearStr}</strong></div>
                <div><span style="color:var(--muted)">Coordinates:</span> <strong>${coordsStr}</strong></div>
                <div><span style="color:var(--muted)">Accuracy:</span> <strong>${accStr}</strong></div>
                <div><span style="color:var(--muted)">PLMN:</span> <strong>${tw.mcc || '--'}-${tw.mnc || '--'}</strong></div>
                <div><span style="color:var(--muted)">Source:</span> <strong style="text-transform: capitalize;">${srcStr}</strong></div>
            </div>
            <div style="font-size: 11px; color: var(--muted); margin-bottom: 6px;">
                Free-Space Path Loss estimate: ${fsplStr}
            </div>
            ${cellsHtml}
        `;

        $('#td-content').innerHTML = html;
        card.classList.remove('hidden');
    }

    // --- Event binding ---
    function bindEvents() {
        // Global unlock on first user interaction for Web Audio autoplay policy
        document.addEventListener('click', unlockAudio, { passive: true });
        document.addEventListener('touchstart', unlockAudio, { passive: true });

        // Mode tabs
        $$('.tab').forEach(function (tab) {
            tab.addEventListener('click', function () {
                unlockAudio();
                const mode = this.dataset.mode;
                setActiveTab(mode);
            });
        });

        // Measure
        $('#measure-btn').addEventListener('click', function () {
            unlockAudio();
            postJSON('/api/session/measure', {});
        });

        // Re-measure
        $('#remeasure-btn').addEventListener('click', function () {
            unlockAudio();
            postJSON('/api/session/remeasure', {});
        });

        // Skip
        $('#skip-btn').addEventListener('click', function () {
            unlockAudio();
            postJSON('/api/session/skip', {});
        });

        // Mark (live mode)
        $('#mark-btn').addEventListener('click', function () {
            unlockAudio();
            postJSON('/api/session/mark', {});
        });

        // Reset
        $('#reset-btn').addEventListener('click', function () {
            if (confirm('Reset all measurements?')) {
                lastInstructionKey = null;
                lastInstruction = null;
                lastWarnedTowerPosId = null;
                lastLiveBestScore = 0;
                lastLiveTrend = 'STEADY';
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
            unlockAudio();
            soundEnabled = !soundEnabled;
            localStorage.setItem('antenna_sound', soundEnabled ? 'true' : 'false');
            updateSoundUI();
            if (soundEnabled) {
                playInstructionSound();
            }
        });

        // Vibration toggle
        $('#vibe-toggle').addEventListener('click', function () {
            if (!isVibeSupported()) return;
            vibeEnabled = !vibeEnabled;
            localStorage.setItem('antenna_vibrate', vibeEnabled ? 'true' : 'false');
            updateVibeUI();
            if (vibeEnabled) {
                vibrate(100);
            }
        });

        // Mock controls
        $$('[data-move]').forEach(function (btn) {
            btn.addEventListener('click', function () {
                postJSON('/api/mock/move', { direction: this.dataset.move });
            });
        });

        // Towers panel controls:
        // 1. Location
        const btnSaveLoc = $('#btn-save-loc');
        if (btnSaveLoc) {
            btnSaveLoc.addEventListener('click', function () {
                const lat = parseFloat($('#loc-lat').value);
                const lon = parseFloat($('#loc-lon').value);
                if (isNaN(lat) || isNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180) {
                    $('#loc-status').textContent = 'Please enter valid coordinates (-90 to 90, -180 to 180)';
                    return;
                }
                saveLocation(lat, lon);
            });
        }

        function stopGpsWatch() {
            if (gpsWatchId !== null) {
                navigator.geolocation.clearWatch(gpsWatchId);
                gpsWatchId = null;
            }
            if (gpsTimeoutId !== null) {
                clearTimeout(gpsTimeoutId);
                gpsTimeoutId = null;
            }
        }

        function startGpsCapture() {
            if (!('geolocation' in navigator)) {
                $('#loc-status').textContent = 'Geolocation not supported. Enter coordinates manually.';
                return;
            }
            if (window.isSecureContext === false) {
                $('#loc-status').textContent = 'Geolocation unavailable (requires HTTPS or localhost). Please enter coordinates manually.';
                return;
            }

            stopGpsWatch();
            bestGpsFix = null;
            pendingLowAccFix = null;
            const warnEl = $('#loc-accuracy-warning');
            if (warnEl) warnEl.classList.add('hidden');
            $('#loc-status').textContent = 'Acquiring GPS position…';

            const startTime = Date.now();
            const maxDurationMs = 15000;

            function evaluateFinalFix() {
                stopGpsWatch();
                if (!bestGpsFix) {
                    $('#loc-status').textContent = 'Could not obtain a GPS fix. Please enter manually.';
                    return;
                }

                const lat = bestGpsFix.coords.latitude;
                const lon = bestGpsFix.coords.longitude;
                const acc = bestGpsFix.coords.accuracy;

                $('#loc-lat').value = lat.toFixed(6);
                $('#loc-lon').value = lon.toFixed(6);

                if (acc > 200) {
                    pendingLowAccFix = { lat: lat, lon: lon, acc: acc };
                    const msgEl = $('#loc-accuracy-msg');
                    if (msgEl) msgEl.textContent = `Low accuracy (±${Math.round(acc)} m). Save anyway?`;
                    if (warnEl) warnEl.classList.remove('hidden');
                    $('#loc-status').textContent = `Coarse fix obtained (±${Math.round(acc)} m).`;
                } else {
                    $('#loc-status').textContent = `GPS fix obtained (±${Math.round(acc)} m). Saving…`;
                    saveLocation(lat, lon, acc);
                }
            }

            gpsTimeoutId = setTimeout(evaluateFinalFix, maxDurationMs);

            gpsWatchId = navigator.geolocation.watchPosition(
                function (pos) {
                    const acc = pos.coords.accuracy;
                    const elapsedSec = Math.round((Date.now() - startTime) / 1000);
                    const remainingSec = Math.max(0, 15 - elapsedSec);

                    if (!bestGpsFix || acc < bestGpsFix.coords.accuracy) {
                        bestGpsFix = pos;
                    }

                    const bestAcc = bestGpsFix.coords.accuracy;
                    $('#loc-status').textContent = `Acquiring GPS… best: ±${Math.round(bestAcc)} m (${remainingSec}s)`;

                    if (bestAcc <= 30) {
                        evaluateFinalFix();
                    }
                },
                function (err) {
                    stopGpsWatch();
                    let msg = '';
                    switch (err.code) {
                        case 1:
                            msg = 'Permission denied. Allow location for this site.';
                            break;
                        case 2:
                            msg = 'Position unavailable.';
                            break;
                        case 3:
                            msg = 'Timed out. Move outdoors or enter manually.';
                            break;
                        default:
                            msg = 'Geolocation error: ' + (err.message || 'Unknown error');
                    }
                    if (window.isSecureContext === false) {
                        msg += ' (Requires HTTPS or localhost)';
                    }
                    $('#loc-status').textContent = msg;
                },
                { enableHighAccuracy: true, maximumAge: 0, timeout: 15000 }
            );
        }

        const btnUseGPS = $('#btn-use-gps');
        if (btnUseGPS) {
            btnUseGPS.addEventListener('click', startGpsCapture);
        }

        const btnGpsSaveLow = $('#btn-gps-save-low');
        if (btnGpsSaveLow) {
            btnGpsSaveLow.addEventListener('click', function () {
                if (pendingLowAccFix) {
                    const warnEl = $('#loc-accuracy-warning');
                    if (warnEl) warnEl.classList.add('hidden');
                    saveLocation(pendingLowAccFix.lat, pendingLowAccFix.lon, pendingLowAccFix.acc);
                    pendingLowAccFix = null;
                }
            });
        }

        const btnGpsRetry = $('#btn-gps-retry');
        if (btnGpsRetry) {
            btnGpsRetry.addEventListener('click', function () {
                const warnEl = $('#loc-accuracy-warning');
                if (warnEl) warnEl.classList.add('hidden');
                pendingLowAccFix = null;
                startGpsCapture();
            });
        }

        const btnRecenter = $('#btn-recenter-map');
        if (btnRecenter) {
            btnRecenter.addEventListener('click', function () {
                if (activeTowerView !== 'map') {
                    switchTowerView('map');
                }
                renderLeafletMap(true);
            });
        }

        // 2. View toggles
        const btnRadial = $('#btn-view-radial');
        if (btnRadial) {
            btnRadial.addEventListener('click', function () {
                switchTowerView('radial');
            });
        }

        const btnMap = $('#btn-view-map');
        if (btnMap) {
            btnMap.addEventListener('click', function () {
                switchTowerView('map');
            });
        }

        // 3. Operators filter
        const chkOps = $('#chk-show-all-ops');
        if (chkOps) {
            chkOps.addEventListener('change', function () {
                showAllOperators = this.checked;
                fetchTowers(showAllOperators);
            });
        }

        // 4. Refresh database
        const btnRefresh = $('#btn-refresh-towers');
        if (btnRefresh) {
            btnRefresh.addEventListener('click', async function () {
                const btn = this;
                btn.style.opacity = '0.5';
                const res = await postJSON('/api/towers/refresh', {});
                btn.style.opacity = '1';
                if (res && !res.ok && res.error) {
                    $('#loc-status').textContent = res.error;
                } else {
                    fetchTowers(showAllOperators);
                }
            });
        }

        // 5. Manual tower entry form
        const btnAddTower = $('#btn-add-tower');
        if (btnAddTower) {
            btnAddTower.addEventListener('click', function () {
                const form = $('#manual-tower-form');
                if (form.classList.contains('hidden')) {
                    openManualTowerModal();
                } else {
                    form.classList.add('hidden');
                }
            });
        }

        const btnCancelManual = $('#btn-cancel-manual-tower');
        if (btnCancelManual) {
            btnCancelManual.addEventListener('click', function () {
                $('#manual-tower-form').classList.add('hidden');
            });
        }

        const btnSaveManual = $('#btn-save-manual-tower');
        if (btnSaveManual) {
            btnSaveManual.addEventListener('click', async function () {
                const lat = parseFloat($('#man-lat').value);
                const lon = parseFloat($('#man-lon').value);
                const enb = parseInt($('#man-enb').value, 10);
                const curMcc = (state && state.sample && state.sample.mcc) ? state.sample.mcc : 606;
                const curMnc = (state && state.sample && state.sample.mnc != null) ? state.sample.mnc : 1;
                const mcc = parseInt($('#man-mcc').value, 10) || curMcc;
                const mnc = parseInt($('#man-mnc').value, 10) || curMnc;
                const acc = parseInt($('#man-acc').value, 10) || 100;

                if (isNaN(lat) || isNaN(lon) || isNaN(enb) || enb <= 0) {
                    alert('Please enter valid latitude, longitude, and eNB ID.');
                    return;
                }

                const payload = {
                    lat: lat,
                    lon: lon,
                    enb: enb,
                    mcc: mcc,
                    mnc: mnc,
                    accuracy: acc,
                    source: 'manual'
                };

                const res = await postJSON('/api/towers/manual', payload);
                if (res && res.ok) {
                    $('#manual-tower-form').classList.add('hidden');
                    fetchTowers(showAllOperators);
                } else {
                    alert('Failed to save tower: ' + ((res && res.error) || 'unknown error'));
                }
            });
        }

        // 6. Close tower details card
        const btnCloseTD = $('#td-close');
        if (btnCloseTD) {
            btnCloseTD.addEventListener('click', function () {
                $('#tower-detail-card').classList.add('hidden');
                selectedTower = null;
            });
        }

        // 7. Click delegation for Radial SVG markers
        const radialSvg = $('#radial-svg');
        if (radialSvg) {
            radialSvg.addEventListener('click', function (e) {
                const marker = e.target.closest('[data-tower-id]');
                if (!marker) return;
                const tid = marker.dataset.towerId;
                const tw = findTowerById(tid);
                if (tw) {
                    showTowerDetails(tw);
                }
            });
        }

        // 8. Click delegation for nearby table rows
        const nearbyBody = $('#nearby-body');
        if (nearbyBody) {
            nearbyBody.addEventListener('click', function (e) {
                const row = e.target.closest('tr[data-tower-id]');
                if (!row) return;
                const tid = row.dataset.towerId;
                const tw = findTowerById(tid);
                if (tw) {
                    showTowerDetails(tw);
                }
            });
        }

        // 9. Full scan offer button in guided mode
        const btnFullScan = $('#btn-full-scan');
        if (btnFullScan) {
            btnFullScan.addEventListener('click', function () {
                postJSON('/api/session/full-scan', {});
            });
        }
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
            return data;
        } catch (e) {
            console.error('Request failed:', e);
            return null;
        }
    }

    // --- Audio & Vibration Engine ---
    function isVibeSupported() {
        return ('vibrate' in navigator) && typeof navigator.vibrate === 'function';
    }

    function getAudioContext() {
        if (!audioCtx) {
            const AudioContextClass = window.AudioContext || window.webkitAudioContext;
            if (AudioContextClass) {
                audioCtx = new AudioContextClass();
            }
        }
        if (audioCtx && audioCtx.state === 'suspended') {
            audioCtx.resume().catch(() => {});
        }
        return audioCtx;
    }

    function unlockAudio() {
        getAudioContext();
    }

    function playTone(freq, duration = 0.12, type = 'sine', gainLevel = 0.25) {
        if (!soundEnabled) return;
        try {
            const ctx = getAudioContext();
            if (!ctx) return;
            const now = ctx.currentTime;
            const osc = ctx.createOscillator();
            const gain = ctx.createGain();

            osc.type = type;
            osc.frequency.setValueAtTime(freq, now);

            gain.gain.setValueAtTime(0.0001, now);
            gain.gain.exponentialRampToValueAtTime(gainLevel, now + 0.015);
            gain.gain.exponentialRampToValueAtTime(0.0001, now + duration);

            osc.connect(gain);
            gain.connect(ctx.destination);

            osc.start(now);
            osc.stop(now + duration + 0.02);
        } catch (e) {
            // Audio not available
        }
    }

    function playSequence(notes) {
        if (!soundEnabled) return;
        try {
            const ctx = getAudioContext();
            if (!ctx) return;
            const baseTime = ctx.currentTime;
            let offset = 0;

            for (const item of notes) {
                const freq = typeof item === 'number' ? item : item.freq;
                const duration = item.duration || 0.09;
                const delay = item.delay != null ? item.delay : offset;
                const gainLevel = item.gain != null ? item.gain : 0.22;
                const type = item.type || 'sine';

                const startTime = baseTime + delay;
                const osc = ctx.createOscillator();
                const gain = ctx.createGain();

                osc.type = type;
                osc.frequency.setValueAtTime(freq, startTime);

                gain.gain.setValueAtTime(0.0001, startTime);
                gain.gain.exponentialRampToValueAtTime(gainLevel, startTime + 0.015);
                gain.gain.exponentialRampToValueAtTime(0.0001, startTime + duration);

                osc.connect(gain);
                gain.connect(ctx.destination);

                osc.start(startTime);
                osc.stop(startTime + duration + 0.02);

                offset = delay + duration + 0.02;
            }
        } catch (e) {
            // Audio not available
        }
    }

    function vibrate(pattern) {
        if (!vibeEnabled || !isVibeSupported()) return;
        try {
            navigator.vibrate(pattern);
        } catch (e) {
            // Safe ignore
        }
    }

    function playInstructionSound() {
        // Two-tone rising prompt: 659Hz -> 880Hz
        playSequence([
            { freq: 659, duration: 0.08, gain: 0.22 },
            { freq: 880, duration: 0.12, gain: 0.25 }
        ]);
    }

    function vibrateInstruction() {
        vibrate(120);
    }

    function playMeasureCompleteSound() {
        // Quick double chirp: 740Hz -> 988Hz
        playSequence([
            { freq: 740, duration: 0.06, gain: 0.2 },
            { freq: 988, duration: 0.09, gain: 0.22 }
        ]);
    }

    function vibrateMeasureComplete() {
        vibrate([80, 50, 80]);
    }

    function playSuccessSound() {
        // Triumphant fanfare: C5 (523Hz), E5 (659Hz), G5 (784Hz), C6 (1046Hz)
        playSequence([
            { freq: 523.25, duration: 0.09, gain: 0.22 },
            { freq: 659.25, duration: 0.09, gain: 0.22 },
            { freq: 783.99, duration: 0.10, gain: 0.25 },
            { freq: 1046.50, duration: 0.24, gain: 0.28 }
        ]);
    }

    function vibrateSuccess() {
        vibrate([100, 60, 100, 60, 250]);
    }

    function playWarningSound() {
        // Low caution alert: 330Hz -> 260Hz
        playSequence([
            { freq: 330, duration: 0.12, type: 'triangle', gain: 0.25 },
            { freq: 260, duration: 0.18, type: 'triangle', gain: 0.25 }
        ]);
    }

    function vibrateWarning() {
        vibrate([180, 80, 180]);
    }

    function playLiveWarmerSound() {
        playTone(920, 0.07, 'sine', 0.18);
    }

    function vibrateLiveWarmer() {
        vibrate(40);
    }

    function playLiveNewBestSound() {
        playSequence([
            { freq: 880, duration: 0.07, gain: 0.22 },
            { freq: 1174.66, duration: 0.12, gain: 0.25 }
        ]);
    }

    function vibrateLiveNewBest() {
        vibrate([60, 40, 80]);
    }

    function playBeep() {
        playInstructionSound();
    }

    function updateSoundUI() {
        const btn = $('#sound-toggle');
        if (!btn) return;
        btn.textContent = soundEnabled ? '🔊' : '🔇';
        btn.title = soundEnabled ? 'Sound: On (Click to mute)' : 'Sound: Muted (Click to enable)';
        btn.setAttribute('aria-label', btn.title);
    }

    function updateVibeUI() {
        const btn = $('#vibe-toggle');
        if (!btn) return;
        if (!isVibeSupported()) {
            btn.textContent = '📴';
            btn.classList.add('unsupported');
            btn.title = 'Vibration not supported on this device/browser';
            btn.setAttribute('aria-label', btn.title);
            return;
        }
        btn.textContent = vibeEnabled ? '📳' : '📴';
        btn.classList.remove('unsupported');
        btn.title = vibeEnabled ? 'Vibration: On (Click to turn off)' : 'Vibration: Off (Click to enable)';
        btn.setAttribute('aria-label', btn.title);
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
