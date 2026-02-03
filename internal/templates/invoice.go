package templates

import (
	"fmt"
	"time"
)

// InvoiceTemplateData holds data for invoice message templates
type InvoiceTemplateData struct {
	ClientName      string
	InvoiceNumber   string
	DueDate         string
	Status          string // Display status (e.g., "Belum Lunas")
	StatusKey       string // Raw key (e.g., "unpaid", "overdue")
	RemainingAmount string // Formatted amount (e.g., "Rp 1.000.000")
}

// GenerateInvoiceMessage generates a WhatsApp message based on invoice status
func GenerateInvoiceMessage(data InvoiceTemplateData) string {
	var header, body, footer string

	switch data.StatusKey {
	case "paid":
		header = fmt.Sprintf("Yth. %s,\n\n✅ *PEMBAYARAN BERHASIL DIKONFIRMASI*\n\nTerima kasih! Pembayaran untuk Invoice *%s* telah berhasil kami terima.",
			data.ClientName, data.InvoiceNumber)
		body = fmt.Sprintf("\n📋 Status: *LUNAS* ✅\n📅 Tanggal: %s",
			time.Now().Format("02 January 2006"))
		footer = "\nDokumen lunas terlampir sebagai arsip Anda. Terima kasih atas kepercayaan Anda memilih Valpro Intertech!\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan otomatis dari Valpro Intertech System_\n\n🌐 Kunjungi: valprointertech.com\n💼 Layanan lainnya tersedia di website kami"

	case "overdue":
		header = fmt.Sprintf("🚨 *TAGIHAN MELEWATI JATUH TEMPO* 🚨\n\nYth. %s,\n\nKami ingin mengingatkan bahwa invoice berikut telah melewati tanggal jatuh tempo:",
			data.ClientName)
		body = fmt.Sprintf("\n📄 No. Invoice: *%s*\n📅 Jatuh Tempo: %s ❌\n💰 Sisa Tagihan: *%s*\n⚠️ Status: *TERLAMBAT*",
			data.InvoiceNumber, data.DueDate, data.RemainingAmount)
		footer = "\n\n*Mohon segera lakukan pembayaran* untuk menyelesaikan tagihan ini.\n\nJika pembayaran sudah dilakukan, mohon konfirmasi dengan mengirimkan bukti transfer.\n\nJika ada kendala, silakan hubungi kami untuk diskusi solusi pembayaran.\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan otomatis dari Valpro Intertech System_\n\n📞 Pertanyaan? Hubungi kami:\n🌐 valprointertech.com\n📧 mail@valprointertech.com\n📱 +62 813-9971-0085\n\n_Terima kasih atas perhatian dan kerjasamanya._"

	case "partial":
		header = fmt.Sprintf("Yth. %s,\n\n💳 *PEMBAYARAN SEBAGIAN DITERIMA*\n\nTerima kasih atas pembayaran sebagian yang telah kami terima.",
			data.ClientName)
		body = fmt.Sprintf("\n📄 No. Invoice: %s\n📅 Jatuh Tempo: %s\n📊 Status: %s\n💰 Sisa Tagihan: %s",
			data.InvoiceNumber, data.DueDate, data.Status, data.RemainingAmount)
		footer = "\nMohon segera melunasi sisa tagihan sebelum tanggal jatuh tempo.\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan otomatis dari Valpro Intertech System_\n\n🌐 Info: valprointertech.com\n📧 mail@valprointertech.com"

	case "draft":
		header = fmt.Sprintf("Yth. %s,\n\n📋 *DRAFT INVOICE*\n\nBerikut draft invoice untuk direview.",
			data.ClientName)
		body = fmt.Sprintf("\n📄 No. Invoice: %s\n📅 Jatuh Tempo: %s\n📊 Status: %s\n💰 Total: %s",
			data.InvoiceNumber, data.DueDate, data.Status, data.RemainingAmount)
		footer = "\nMohon konfirmasinya apabila sudah sesuai.\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan otomatis dari Valpro Intertech System_\n\n🌐 valprointertech.com\n📧 mail@valprointertech.com"

	case "reminder":
		header = fmt.Sprintf("🔔 *REMINDER PEMBAYARAN* 🔔\n\nYth. %s,",
			data.ClientName)
		body = fmt.Sprintf("\nMengingatkan kembali bahwa Invoice *%s* akan jatuh tempo besok (%s).\n\n💰 Sisa Tagihan: %s",
			data.InvoiceNumber, data.DueDate, data.RemainingAmount)
		footer = "\nMohon segera dilakukan pembayaran. Abaikan pesan ini jika sudah membayar.\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan otomatis dari Valpro Intertech System_\n\n🌐 valprointertech.com\n📱 Butuh bantuan? +62 813-9971-0085"

	default: // unpaid, or any other status
		header = fmt.Sprintf("Yth. %s,\n\nTerlampir dokumen tagihan dari *Valpro Intertech*.",
			data.ClientName)
		body = fmt.Sprintf("\n📄 No. Invoice: %s\n📅 Jatuh Tempo: %s\n📊 Status: %s\n💰 Sisa Tagihan: %s",
			data.InvoiceNumber, data.DueDate, data.Status, data.RemainingAmount)
		footer = "\nMohon segera diselesaikan. Terima kasih atas kepercayaan Anda.\n\n━━━━━━━━━━━━━━━━━━━\n🤖 _Pesan ini dikirim secara otomatis oleh Valpro Intertech System_\n\n📞 Info lebih lanjut hubungi:\n🌐 valprointertech.com\n📧 mail@valprointertech.com\n📱 +62 813-9971-0085"
	}

	return header + body + footer
}

// GenerateOTPMessage generates an OTP message for authentication
func GenerateOTPMessage(otp string) string {
	return fmt.Sprintf("🔐 *Kode Login ValproCloud*\n\nKode OTP Anda: *%s*\n\nJangan berikan kode ini kepada siapapun.\nBerlaku 5 menit.", otp)
}

// GenerateBroadcastMessage generates a broadcast message with optional variations
func GenerateBroadcastMessage(recipientName, message string, useVariation bool) string {
	if !useVariation {
		return message
	}

	// Anti-bot: Random greeting variations
	greetings := []string{"Yth.", "Dear", "Kepada Yth.", "Halo"}
	greeting := greetings[time.Now().UnixNano()%int64(len(greetings))]

	return fmt.Sprintf("%s %s,\n\n%s", greeting, recipientName, message)
}

// GenerateBackupNotification generates a backup notification message
func GenerateBackupNotification(success bool, filename string, timestamp time.Time) string {
	if success {
		return fmt.Sprintf("✅ *BACKUP BERHASIL*\n\n📁 File: %s\n🕐 Waktu: %s\n\nBackup data harian telah berhasil disimpan.",
			filename, timestamp.Format("02 Jan 2006 15:04 WIB"))
	}
	return fmt.Sprintf("❌ *BACKUP GAGAL*\n\n🕐 Waktu: %s\n\nBackup data gagal. Silakan periksa log server.",
		timestamp.Format("02 Jan 2006 15:04 WIB"))
}

// GenerateHealthAlert generates a system health alert message
func GenerateHealthAlert(status string, latency int, timestamp time.Time) string {
	switch status {
	case "recovery":
		return fmt.Sprintf("✅ *SISTEM PULIH*\n\n🕐 %s\n\nSistem Valpro Intertech kembali online setelah mengalami gangguan.",
			timestamp.Format("02 Jan 2006 15:04"))
	case "slow":
		return fmt.Sprintf("⚠️ *SISTEM LAMBAT*\n\n🕐 %s\n⏱️ Latency: %dms\n\nRespon sistem lebih lambat dari normal.",
			timestamp.Format("02 Jan 2006 15:04"), latency)
	case "down":
		return fmt.Sprintf("🚨 *SISTEM DOWN*\n\n🕐 %s\n\nSistem tidak dapat diakses. Tim teknis sedang menangani.",
			timestamp.Format("02 Jan 2006 15:04"))
	default:
		return fmt.Sprintf("ℹ️ *STATUS SISTEM*\n\n🕐 %s\n📊 Status: %s\n⏱️ Latency: %dms",
			timestamp.Format("02 Jan 2006 15:04"), status, latency)
	}
}
