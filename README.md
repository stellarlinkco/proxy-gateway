# Claude / Codex / Gemini API Proxy

[![GitHub release](https://img.shields.io/github/v/release/stellarlinkco/proxy-gateway)](https://github.com/stellarlinkco/proxy-gateway/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev/)
[![Vue Version](https://img.shields.io/badge/Vue-3.5-4FC08D?logo=vue.js)](https://vuejs.org/)
[![Docker](https://img.shields.io/badge/Docker-Available-2496ED?logo=docker)](https://ghcr.io)

[中文文档](README_ZH.md)

A high-performance multi-protocol AI API proxy server supporting Claude Messages API, Codex Responses API, and Gemini API with intelligent failover, multi-channel scheduling, API key rotation, and unified access.

## Features

### Core Capabilities

- **All-in-One Architecture**: Backend integrates frontend, single container deployment, replaces Nginx
- **Unified Authentication**: Single key protects all endpoints (Web UI, Management API, Proxy API)
- **Web Management Panel**: Modern visual interface for channel management, real-time monitoring, and configuration

### Multi-Protocol Support

- **Claude Messages API** (`/v1/messages`) - Anthropic native protocol
- **Codex Responses API** (`/v1/responses`) - OpenAI compatible protocol with session management
- **Gemini API** (`/v1beta/models/*`) - Google native protocol
- **Models API** (`/v1/models`) - Unified model list query

### Intelligent Scheduling

- **ChannelKind Unified Scheduling**: Three API types (Messages/Responses/Gemini) with unified scheduling architecture
- **Channel Orchestration**: Visual channel management, drag-and-drop priority adjustment, real-time health status
- **Trace Affinity**: Same user session automatically binds to the same channel for consistency
- **Auto Circuit Breaker**: Sliding window algorithm detects channel health, auto-breaks on high failure rate
- **Failover**: Automatically switches to available channels for high availability
- **Multi API Keys**: Each upstream can configure multiple API keys with automatic rotation

### Protocol Conversion

- **Unified Interface**: Clients only need to use Claude Messages API format
- **Auto Conversion**: Proxy automatically handles protocol differences between upstreams (Claude/OpenAI/Gemini)
- **Plug and Play**: No client code changes needed when switching upstream services

### Monitoring & Management

- **Real-time Request Monitoring**: View ongoing requests and historical logs
- **Traffic Statistics**: Monitor request traffic, success rate, and response latency per channel
- **Dual Configuration**: CLI tools and Web interface for upstream configuration
- **Logging System**: Complete request/response logging
- **Session Management**: Responses API supports multi-turn conversation tracking

## Quick Start

### Docker (Recommended)

```bash
# Pull and run the pre-built image
docker run -d \
  --name claude-proxy \
  -p 3000:3000 \
  -e PROXY_ACCESS_KEY=your-super-strong-secret-key \
  -v $(pwd)/.config:/app/.config \
  ghcr.io/stellarlinkco/proxy-gateway:latest
```

Or use docker-compose:

```bash
# Clone the project
git clone https://github.com/stellarlinkco/proxy-gateway
cd claude-proxy

# Edit docker-compose.yml to set PROXY_ACCESS_KEY

# Start the service
docker-compose up -d
```

### Direct Download (Fastest)

Download pre-built binaries from [Releases](https://github.com/stellarlinkco/proxy-gateway/releases/latest):

| OS | Architecture | Filename |
|---|---|---|
| **Windows** | x64 | `claude-proxy-windows-amd64.exe` |
| **Windows** | ARM64 | `claude-proxy-windows-arm64.exe` |
| **macOS** | Intel | `claude-proxy-darwin-amd64` |
| **macOS** | Apple Silicon | `claude-proxy-darwin-arm64` |
| **Linux** | x64 | `claude-proxy-linux-amd64` |
| **Linux** | ARM64 | `claude-proxy-linux-arm64` |

```bash
# Linux / macOS
chmod +x claude-proxy-*
./claude-proxy-linux-amd64

# Windows (PowerShell)
.\claude-proxy-windows-amd64.exe
```

Create `.env` file in the same directory:

```bash
PROXY_ACCESS_KEY=your-super-strong-secret-key
PORT=3000
ENABLE_WEB_UI=true
```

### Build from Source

```bash
git clone https://github.com/stellarlinkco/proxy-gateway
cd claude-proxy

cp backend-go/.env.example backend-go/.env
# Edit backend-go/.env

make run  # or: make dev for hot reload
```

## Access Points

- **Web UI**: http://localhost:3000
- **Messages API**: http://localhost:3000/v1/messages
- **Responses API**: http://localhost:3000/v1/responses
- **Gemini API**: http://localhost:3000/v1beta/models/{model}:generateContent
- **Health Check**: http://localhost:3000/health

## Architecture

```
User → Backend:3000 →
     ├─ /           → Web UI (requires key)
     ├─ /api/*      → Management API (requires key)
     ├─ /v1/messages → Claude Messages API proxy (requires key)
     ├─ /v1/responses → Codex Responses API proxy (requires key)
     ├─ /v1/models  → Models API (requires key)
     └─ /v1beta/models/* → Gemini API proxy (requires key)
```

**Key Benefits**: Single port, unified auth, no CORS issues, low resource usage

## API Usage

### Messages API

```bash
curl -X POST http://localhost:3000/v1/messages \
  -H "x-api-key: your-proxy-access-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

### Streaming Response

```bash
curl -X POST http://localhost:3000/v1/messages \
  -H "x-api-key: your-proxy-access-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "stream": true,
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Count to 10"}
    ]
  }'
```

### Responses API (Session Support)

```bash
curl -X POST http://localhost:3000/v1/responses \
  -H "x-api-key: your-proxy-access-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5",
    "max_tokens": 100,
    "input": "Hello! Please introduce yourself."
  }'
```

### Gemini API

```bash
curl -X POST "http://localhost:3000/v1beta/models/gemini-2.0-flash:generateContent" \
  -H "x-api-key: your-proxy-access-key" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [
      {"role": "user", "parts": [{"text": "Hello!"}]}
    ]
  }'
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PROXY_ACCESS_KEY` | - | **Required** Access key for authentication |
| `PORT` | 3000 | Server port |
| `ENV` | production | Environment (development/production) |
| `ENABLE_WEB_UI` | true | Enable web management UI |
| `LOG_LEVEL` | info | Log level (debug/info/warn/error) |

See [ENVIRONMENT.md](ENVIRONMENT.md) for full configuration options.

## Security

All endpoints (except `/health`) are protected by `PROXY_ACCESS_KEY`:

1. **Web UI** (`/`) - Key via query param or localStorage
2. **Management API** (`/api/*`) - `x-api-key` header required
3. **Proxy API** (`/v1/*`) - `x-api-key` header required
4. **Health Check** (`/health`) - Public access

### Production Security Checklist

```bash
# Generate strong key
PROXY_ACCESS_KEY=$(openssl rand -base64 32)

# Production settings
ENV=production
ENABLE_REQUEST_LOGS=false
ENABLE_RESPONSE_LOGS=false
LOG_LEVEL=warn
```

## CI/CD

GitHub Actions workflows:

| Workflow | Description |
|---|---|
| `release-linux.yml` | Build Linux amd64/arm64 |
| `release-macos.yml` | Build macOS amd64/arm64 |
| `release-windows.yml` | Build Windows amd64/arm64 |
| `docker-build.yml` | Build multi-platform Docker images |

### Release New Version

```bash
echo "vX.Y.Z" > VERSION
git add . && git commit -m "chore: bump version to vX.Y.Z"
git tag vX.Y.Z
git push origin main --tags
```

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) - Architecture design and technical choices
- [ENVIRONMENT.md](ENVIRONMENT.md) - Environment variables and configuration
- [DEVELOPMENT.md](DEVELOPMENT.md) - Development guide and best practices
- [CONTRIBUTING.md](CONTRIBUTING.md) - Contribution guidelines
- [CHANGELOG.md](CHANGELOG.md) - Version history
- [RELEASE.md](RELEASE.md) - Release process

## License

MIT License - see [LICENSE](LICENSE) for details.
