# Antenna Aligner

A single Go program to guide and align a 4G antenna using live metrics from a **Huawei B310s-22** router.
Features a mobile-first web interface designed for outdoor phone use right next to the antenna.

## Features

- **HiLink XML API Integration**: Automatic session management, token rotation, and password hashing (`password_type` 4 and fallback 3).
- **Embedded Web Assets**: Uses `go:embed` for a single self-contained binary (no external assets, CDNs, or build tools needed).
- **Real-Time Updates**: SSE (Server-Sent Events) streaming signal samples and alignment instructions directly to the browser.
- **Guided Alignment Mode**:
  - `COARSE` (30° scan, 12 positions around the circle)
  - `FINE_AZ_10` & `FINE_AZ_5` (Hill-climbing azimuth optimization)
  - `TILT` (Hill-climbing vertical elevation)
  - `RECHECK` & `DONE`
- **Live Mode**: Hot/cold real-time guidance (warmer / steady / colder) with position markers.
- **Signal Quality Metrics**: Composite score weighted across SINR (50%), RSRP (35%), and RSRQ (15%).
- **Interactive Visualizations**: Inline SVG polar chart (12 spokes) and 5-minute rolling signal history graph.
- **Simulation / Mock Mode**: `--mock` flag simulates antenna movement, lag, angular penalty, and tower changes without needing hardware.
- **CSV Export**: One-click export with UTF-8 BOM for Microsoft Excel.

## Quick Start

### 1. Simulated / Mock Mode (Development)
```bash
go run . --mock
```
Open `http://localhost:8080` (or `http://<your-pc-ip>:8080` from a mobile phone on the same Wi-Fi).

### 2. Real Router Mode
Copy the example config:
```bash
cp config.example.json config.json
```
Edit `config.json` with your router credentials and IP, then run:
```bash
go run . --config config.json
```

### 3. Build Single Executable
```bash
go build -o antenna-aligner .
```

## Security Note

> [!NOTE]
> This application binds to your local network (`:8080` by default) without authentication on the web interface so you can easily access it on your phone. Run it only on trusted local home/private networks. Never commit your real `config.json` containing the router password.
