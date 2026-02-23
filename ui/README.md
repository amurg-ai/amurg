# Amurg UI

React web client for Amurg. Mobile-first chat interface with voice input, rich rendering, and real-time WebSocket updates.

## Requirements

- Node.js 20+
- npm 9+

## Development

```bash
cd ui
npm install
npm run dev
```

Opens on http://localhost:3000. The Vite dev server proxies `/api` and `/ws` to the Hub at `localhost:8090`.

## Build

```bash
npm run build
# Output: ui/dist/
```

The production build produces two code-split chunks:
- Main bundle (~290 KB gzipped ~93 KB)
- Markdown renderer (~349 KB gzipped ~106 KB, lazy-loaded)

## Deployment

The UI is static files. Two deployment options:

**Served by the Hub** (recommended):
Set `server.ui_static_dir` in the Hub config to point to `ui/dist/`. The Hub Dockerfile does this automatically.

**Standalone** (behind a reverse proxy):
Serve `ui/dist/` from nginx/caddy and proxy `/api/*` and `/ws` to the Hub.

Example nginx config:
```nginx
server {
    listen 443 ssl;
    root /var/www/amurg/ui;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location /api/ {
        proxy_pass http://hub:8080;
    }

    location /ws {
        proxy_pass http://hub:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

## Tests

```bash
npm run test        # Watch mode
npx vitest run      # Single run (57 tests)
```

## Voice Input

The UI supports two speech-to-text backends, configurable via the gear icon on the mic button:

**Browser (default):** Uses the Web Speech API. Works in Chrome/Edge. Zero setup.

**Local Whisper:** Connects to a local Whisper WebSocket server for private, offline transcription. Compatible with:
- [WhisperLiveKit](https://github.com/QuentinFuxa/WhisperLiveKit) — `pip install whisperlivekit && wlk --model base`
- [whisper_streaming](https://github.com/E-Sensia/whisper_streaming)
- Any server accepting audio via WebSocket at `/asr`

Configure the URL (e.g. `ws://localhost:8000/asr`) in the voice settings popover.

## Tech Stack

- React 19 + TypeScript
- Vite 6 (build + dev server)
- Tailwind CSS 4
- Zustand (state management)
- react-markdown + rehype (rich rendering)
- ansi-to-react (ANSI terminal output)
- highlight.js (syntax highlighting)
