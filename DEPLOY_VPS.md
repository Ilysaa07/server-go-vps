# 🚀 Deployment Guide: WhatsApp Server (Go) on Ubuntu VPS

This guide explains how to deploy the `wa-server-go` application to a production Ubuntu server.

## 📋 Prerequisites
*   Ubuntu VPS (20.04 or 22.04 LTS recommended)
*   Root access or Sudo privileges
*   Domain name pointing to your VPS IP (e.g., `wa.yourdomain.com`)

## 🛠️ Step 1: Prepare the Server

Connect to your VPS and update the system:
```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y build-essential gcc git curl
```

## 📦 Step 2: Install Go (Golang)

Since we use `CGO` (SQLite), it's best to build on the Linux server directly.

1.  **Download & Install Go 1.24.0**:
    ```bash
    wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
    ```
2.  **Add to Path**:
    Add these lines to `~/.bashrc` or `~/.profile`:
    ```bash
    export PATH=$PATH:/usr/local/go/bin
    export GOPATH=$HOME/go
    export PATH=$PATH:$GOPATH/bin
    ```
3.  **Reload**:
    ```bash
    source ~/.bashrc
    go version  # Should show go1.24.0
    ```

## 📂 Step 3: Clone Code as User 'devalpro'

Run this as your specialized user (NOT root):

```bash
su - devalpro
git clone https://github.com/Ilysaa07/server-go.git
cd server-go
```

## 🔑 Step 4: Configure Environment & Credentials

1.  **Create `.env` file**:
    ```bash
    cp .env.example .env
    nano .env
    ```
    *   Set `PORT=8080` (or 3000)
    *   Set `WA_API_KEY` (Strong secret)

2.  **Upload Credentials**:
    Use SCP to upload your Firebase JSON to simple path:
    ```bash
    scp firebase.json devalpro@VPS_IP:/home/devalpro/server-go/
    ```

## 🏗️ Step 5: Build the Application

```bash
go mod tidy
go build -o wa-server ./cmd/server
```

## 🤖 Step 6: Setup System Service (Auto-Start)

Exit back to root to set up the service:
```bash
exit # Go back to root
sudo nano /etc/systemd/system/wa-server.service
```

Paste the updated config for `devalpro`:
```ini
[Unit]
Description=WhatsApp Go Server
After=network.target

[Service]
User=devalpro
WorkingDirectory=/home/devalpro/server-go
ExecStart=/home/devalpro/server-go/wa-server
Restart=always
RestartSec=10
Environment=GIN_MODE=release

[Install]
WantedBy=multi-user.target
```

Enable and Start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable wa-server
sudo systemctl start wa-server
```

## 🔒 Step 7: Setup Nginx & SSL (HTTPS)

Don't expose port 8080 directly. Use Nginx.

1.  **Install Nginx**:
    ```bash
    sudo apt install -y nginx certbot python3-certbot-nginx
    ```
2.  **Configure Nginx**:
    ```bash
    sudo nano /etc/nginx/sites-available/wa-server
    ```
    Content:
    ```nginx
    server {
        server_name wa.yourdomain.com; # Change this

        location / {
            proxy_pass http://localhost:8080;
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
        }
    }
    ```
3.  **Enable Site**:
    ```bash
    sudo ln -s /etc/nginx/sites-available/wa-server /etc/nginx/sites-enabled/
    sudo nginx -t
    sudo systemctl restart nginx
    ```
4.  **SSL (HTTPS)**:
    ```bash
    sudo certbot --nginx -d wa.yourdomain.com
    ```

## ✅ Done!
Your WhatsApp server is now Live at `https://wa.yourdomain.com`.
