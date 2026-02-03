# wa-server-go

WhatsApp Server built with Go and whatsmeow.

## Prerequisites

- Go 1.21+
- SQLite3 (for session storage)
- Firebase Service Account JSON (for Firestore)

## Setup

1. **Install dependencies:**

```bash
cd wa-server-go

# Core dependencies
go get github.com/gin-gonic/gin@v1.9.1
go get github.com/gorilla/websocket@v1.5.1
go get github.com/joho/godotenv@v1.5.1
go get github.com/mattn/go-sqlite3@v1.14.19
go get github.com/skip2/go-qrcode

# whatsmeow (latest)
go get go.mau.fi/whatsmeow@latest

# Finalize
go mod tidy
```

2. **Configure environment:**

```bash
cp .env.example .env
# Edit .env with your settings
```

3. **Add Firebase Service Account:**

Place your Firebase service account JSON as `firebase-sa.json` in the project root.

4. **Build and run:**

```bash
go build -o wa-server-go ./cmd/server
./wa-server-go
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/` | Health check |
| GET | `/status` | Detailed status |
| POST | `/send-invoice` | Send invoice + PDF |
| POST | `/send-message` | Send text message |
| POST | `/send-media` | Send media from URL |
| GET | `/get-chats` | List recent chats |
| GET | `/get-messages/:chatId` | Chat history |
| GET | `/get-media/:messageId` | Download media |
| POST | `/sync-contacts` | Sync contacts from Firestore |
| POST | `/trigger-backup` | Manual backup trigger |

## WebSocket

Connect to `/ws` for real-time events:
- `qr-image` - QR code for authentication
- `status-update` - Connection status changes
- `new-message` - Incoming messages

## Environment Variables

See `.env.example` for all configuration options.

## Architecture

```
wa-server-go/
├── cmd/server/main.go      # Entry point
├── internal/
│   ├── config/             # Configuration
│   ├── whatsapp/           # WhatsApp client wrapper
│   ├── api/                # HTTP handlers
│   │   ├── handlers/       # Route handlers
│   │   ├── middleware/     # Auth, CORS
│   │   └── websocket/      # Real-time hub
│   └── utils/              # Utilities
└── firebase-sa.json        # Firebase credentials
```
