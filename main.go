package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ============================================================
// STRUCTS
// ============================================================

type CheckNameRequest struct {
	NamaPT    string `json:"namaPT"`
	Singkatan string `json:"singkatan"`
}

type SimilarName struct {
	No       string `json:"no"`
	NameHTML string `json:"nameHtml"`
	Status   string `json:"status"`
}

type CheckNameResponse struct {
	Success      bool          `json:"success"`
	Available    bool          `json:"available,omitempty"`
	Message      string        `json:"message"`
	Warning      string        `json:"warning,omitempty"`
	SimilarNames []SimilarName `json:"similarNames,omitempty"`
}

// ============================================================
// CORS MIDDLEWARE
// ============================================================

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
		if allowedOrigins == "" {
			allowedOrigins = "https://valprointertech.com,https://www.valprointertech.com"
		}

		origin := r.Header.Get("Origin")
		for _, allowed := range strings.Split(allowedOrigins, ",") {
			if strings.TrimSpace(allowed) == origin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				break
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

// ============================================================
// AHU SCRAPER using chromedp
// ============================================================

func checkNameAHU(namaPT, singkatan string) (*CheckNameResponse, error) {
	// Create a new browser context with timeout
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 800),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	// Set overall timeout to 90 seconds
	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var (
		isAvailable bool
		alertText   string
		warningText string
		similarJSON string
	)

	log.Printf("[AHU] Memulai pengecekan untuk nama: %s", namaPT)

	// Step 1: Navigate and wait for page to load
	err := chromedp.Run(ctx,
		chromedp.Navigate("https://ahu.go.id/sabh/perseroan/pesannama"),
		chromedp.WaitVisible("#inputNama", chromedp.ByID),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal memuat halaman AHU: %w", err)
	}

	log.Println("[AHU] Halaman berhasil dimuat, mengisi form...")

	// Step 2: Fill in the form fields
	actions := []chromedp.Action{
		chromedp.Clear("#inputNama", chromedp.ByID),
		chromedp.SendKeys("#inputNama", namaPT, chromedp.ByID),
	}

	if singkatan != "" {
		actions = append(actions,
			chromedp.Clear("#inputNamaSingkat", chromedp.ByID),
			chromedp.SendKeys("#inputNamaSingkat", singkatan, chromedp.ByID),
		)
	}

	err = chromedp.Run(ctx, actions...)
	if err != nil {
		return nil, fmt.Errorf("gagal mengisi form: %w", err)
	}

	log.Println("[AHU] Form terisi, mengklik tombol cari...")

	// Step 3: Click search button
	err = chromedp.Run(ctx,
		chromedp.Click("#order-name", chromedp.ByID),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal mengklik tombol cari: %w", err)
	}

	// Step 4: Wait for result alert to appear (with retry logic)
	log.Println("[AHU] Menunggu hasil dari AHU...")

	err = chromedp.Run(ctx,
		chromedp.WaitVisible(".body-panel .alert", chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("timeout menunggu hasil dari AHU: %w", err)
	}

	// Step 5: Extract results using JavaScript
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
			const panel = document.querySelector('.body-panel');
			if (!panel) return JSON.stringify({ error: 'panel not found' });
			
			const statusAlert = panel.querySelector('.alert-success, .alert-danger');
			if (!statusAlert) return JSON.stringify({ error: 'alert not found' });
			
			const isAvailable = statusAlert.classList.contains('alert-success');
			const text = statusAlert.innerText.replace(/\s+/g, ' ').trim();
			
			const warningAlert = panel.querySelector('.alert-warning');
			const warningText = warningAlert ? warningAlert.innerText.replace(/\s+/g, ' ').trim() : '';
			
			const similarNames = [];
			const rows = panel.querySelectorAll('table tbody tr');
			rows.forEach(row => {
				const cols = row.querySelectorAll('td');
				if (cols.length >= 3) {
					similarNames.push({
						no: cols[0].innerText.trim(),
						nameHtml: cols[1].innerHTML.trim(),
						status: cols[2].innerText.trim()
					});
				}
			});
			
			return JSON.stringify({
				isAvailable,
				text,
				warningText,
				similarNames
			});
		})()`, &similarJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("gagal mengekstrak hasil: %w", err)
	}

	log.Printf("[AHU] Raw result: %s", similarJSON)

	// Parse the extracted JSON
	var extracted struct {
		Error        string        `json:"error,omitempty"`
		IsAvailable  bool          `json:"isAvailable"`
		Text         string        `json:"text"`
		WarningText  string        `json:"warningText"`
		SimilarNames []SimilarName `json:"similarNames"`
	}

	if err := json.Unmarshal([]byte(similarJSON), &extracted); err != nil {
		return nil, fmt.Errorf("gagal mem-parse hasil: %w", err)
	}

	if extracted.Error != "" {
		return nil, fmt.Errorf("error dari AHU: %s", extracted.Error)
	}

	isAvailable = extracted.IsAvailable
	alertText = extracted.Text
	warningText = extracted.WarningText

	response := &CheckNameResponse{
		Success:      true,
		Available:    isAvailable,
		Message:      alertText,
		Warning:      warningText,
		SimilarNames: extracted.SimilarNames,
	}

	log.Printf("[AHU] Hasil: available=%v, message=%s", isAvailable, alertText)

	return response, nil
}

// ============================================================
// HTTP HANDLER
// ============================================================

func handleCheckName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(CheckNameResponse{
			Success: false,
			Message: "Method not allowed. Use POST.",
		})
		return
	}

	var req CheckNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CheckNameResponse{
			Success: false,
			Message: "Invalid request body.",
		})
		return
	}

	if strings.TrimSpace(req.NamaPT) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CheckNameResponse{
			Success: false,
			Message: "Nama PT harus diisi.",
		})
		return
	}

	log.Printf("[API] Request: namaPT=%s, singkatan=%s", req.NamaPT, req.Singkatan)

	result, err := checkNameAHU(req.NamaPT, req.Singkatan)
	if err != nil {
		log.Printf("[API] Error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CheckNameResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// ============================================================
// HEALTH CHECK
// ============================================================

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"service": "ahu-checker",
		"time":    time.Now().Format(time.RFC3339),
	})
}

// ============================================================
// MAIN
// ============================================================

func main() {
	port := os.Getenv("AHU_PORT")
	if port == "" {
		port = "8082"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/cek-nama-pt", corsMiddleware(handleCheckName))
	mux.HandleFunc("/health", handleHealth)

	log.Printf("🚀 AHU Checker Server running on port %s", port)
	log.Printf("   Endpoints:")
	log.Printf("   POST /cek-nama-pt   - Cek ketersediaan nama PT")
	log.Printf("   GET  /health        - Health check")

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server gagal jalan: %v", err)
	}
}
