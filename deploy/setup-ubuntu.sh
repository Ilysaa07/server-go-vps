#!/bin/bash

# Auto Setup Script for WhatsApp Go Server on Ubuntu
# Usage: sudo ./setup-ubuntu.sh

set -e

echo "🚀 Starting Setup for WA Server Go..."

# 1. Update System
echo "📦 Updating system packages..."
sudo apt update && sudo apt upgrade -y
sudo apt install -y build-essential gcc git curl make sqlite3

# 2. Install Go 1.24.0
if ! command -v go &> /dev/null; then
    echo "🌟 Installing Go 1.24.0..."
    wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
    rm go1.24.0.linux-amd64.tar.gz
    
    # Setup Paths
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    echo 'export GOPATH=$HOME/go' >> ~/.bashrc
    echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
    export PATH=$PATH:/usr/local/go/bin
else
    echo "✅ Go is already installed."
fi

# 3. Create Directories
echo "📂 Creating project directories..."
mkdir -p uploads/media
chmod 755 uploads/media

# 4. Build Project
echo "🏗️ Building WA Server..."
go mod tidy
go build -o wa-server ./cmd/server

if [ -f "./wa-server" ]; then
    echo "✅ Build Successful!"
else
    echo "❌ Build Failed!"
    exit 1
fi

echo "========================================"
echo "🎉 Setup Complete!"
echo "Next Steps:"
echo "1. Configure .env file (cp .env.example .env)"
echo "2. Copy Systemd service: sudo cp deploy/wa-server.service /etc/systemd/system/"
echo "3. Start Service: sudo systemctl start wa-server"
echo "========================================"
