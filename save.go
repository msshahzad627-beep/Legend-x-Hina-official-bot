package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings" // 🚀 ایڈ کیا گیا تاکہ سٹرنگز کو سپلٹ (clean) کیا جا سکے
	"time"

	_ "github.com/lib/pq"
	"go.mau.fi/whatsmeow"
)

// گلوبل ڈیٹا بیس ویری ایبل
var CRMDB *sql.DB

// ==========================================
// 🛠️ 1. DATABASE INITIALIZATION
// ==========================================
func InitCRMDB() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("⚠️ [CRM DATABASE] DATABASE_URL not found. Saving features disabled silently.")
		return
	}

	var err error
	CRMDB, err = sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Printf("⚠️ [CRM DATABASE] Failed to connect: %v\n", err)
		CRMDB = nil
		return
	}

	if err = CRMDB.Ping(); err != nil {
		fmt.Printf("⚠️ [CRM DATABASE] Ping failed: %v\n", err)
		CRMDB = nil
		return
	}

	fmt.Println("✅ [CRM DATABASE] PostgreSQL Connected Successfully!")
	createCRMTables()
}

func createCRMTables() {
	if CRMDB == nil { return }

	queries := []string{
		`CREATE TABLE IF NOT EXISTS crm_contacts (
			bot_jid TEXT,
			user_jid TEXT,
			push_name TEXT,
			created_at BIGINT,
			PRIMARY KEY (bot_jid, user_jid)
		);`,
		`CREATE TABLE IF NOT EXISTS crm_chats (
			bot_jid TEXT,
			chat_jid TEXT,
			last_message TEXT,
			updated_at BIGINT,
			PRIMARY KEY (bot_jid, chat_jid)
		);`,
		`CREATE TABLE IF NOT EXISTS crm_messages (
			msg_id TEXT PRIMARY KEY,
			bot_jid TEXT,
			chat_jid TEXT,
			sender_jid TEXT,
			message_text TEXT,
			media_url TEXT,
			media_type TEXT,
			quoted_msg_id TEXT,
			quoted_text TEXT,
			quoted_media_type TEXT,
			is_status BOOLEAN,
			timestamp BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS crm_access_keys (
			key_string TEXT PRIMARY KEY,
			allowed_bots INTEGER,
			auto_allow BOOLEAN,
			is_admin BOOLEAN,
			created_at BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS crm_vip_keys (
			key_string TEXT PRIMARY KEY,
			allowed_bots TEXT,
			auto_allow BOOLEAN,
			is_admin BOOLEAN,
			created_at BIGINT
		);`,
	}

	for _, q := range queries {
		_, err := CRMDB.Exec(q)
		if err != nil {
			fmt.Printf("⚠️ [CRM DATABASE] Table creation error: %v\n", err)
		}
	}

	// 🚀 FIX: Auto-Migrate Database (نئے کالمز شامل کرنے کے لیے)
	migrationQueries := []string{
		`ALTER TABLE crm_messages ADD COLUMN IF NOT EXISTS quoted_msg_id TEXT;`,
		`ALTER TABLE crm_messages ADD COLUMN IF NOT EXISTS quoted_text TEXT;`,
		`ALTER TABLE crm_messages ADD COLUMN IF NOT EXISTS quoted_media_type TEXT;`,
	}

	for _, mq := range migrationQueries {
		_, err := CRMDB.Exec(mq)
		if err != nil {
			fmt.Printf("⚠️ [CRM DATABASE] Migration error: %v\n", err)
		}
	}
}


// ==========================================
// 🚀 2. MAIN PROCESSOR & SAVER
// ==========================================
func ProcessAndSaveMessage(client *whatsmeow.Client, botJID, msgID, chatJID, senderJID, pushName, textBody string, isStatus, hasMedia bool, mediaType string, mediaMsg interface{}, timestamp int64, quotedMsgID string, quotedText string, quotedMediaType string) {
	if CRMDB == nil {
		return 
	}

	// 🚀 FIX: ڈیٹا بیس میں جانے سے پہلے سب جے آئی ڈیز کو کلین (Clean) کر دیں
	// اس سے @s.whatsapp.net یا کوئی اور ڈومین ریموو ہو جائے گی اور صرف نمبر بچے گا
	botJID = strings.Split(botJID, "@")[0]
	chatJID = strings.Split(chatJID, "@")[0]
	senderJID = strings.Split(senderJID, "@")[0]

	// 1. کانٹیکٹ سیو یا اپڈیٹ کریں
	if pushName != "" {
		_, _ = CRMDB.Exec(`
			INSERT INTO crm_contacts (bot_jid, user_jid, push_name, created_at) 
			VALUES ($1, $2, $3, $4) 
			ON CONFLICT (bot_jid, user_jid) DO UPDATE SET push_name = EXCLUDED.push_name
		`, botJID, senderJID, pushName, timestamp)
	} else {
		_, _ = CRMDB.Exec(`
			INSERT INTO crm_contacts (bot_jid, user_jid, push_name, created_at) 
			VALUES ($1, $2, $3, $4) 
			ON CONFLICT (bot_jid, user_jid) DO NOTHING
		`, botJID, senderJID, "Unknown", timestamp)
	}

	// 2. چیٹ ہسٹری اپڈیٹ کریں
	if !isStatus {
		previewText := textBody
		if previewText == "" && hasMedia {
			previewText = "📸 " + mediaType 
		}
		
		_, _ = CRMDB.Exec(`
			INSERT INTO crm_chats (bot_jid, chat_jid, last_message, updated_at) 
			VALUES ($1, $2, $3, $4) 
			ON CONFLICT (bot_jid, chat_jid) DO UPDATE SET last_message = EXCLUDED.last_message, updated_at = EXCLUDED.updated_at
		`, botJID, chatJID, previewText, timestamp)
	}

	// 3. میڈیا ہینڈلنگ اور کیٹ باکس اپلوڈ
	mediaURL := ""
	if hasMedia && mediaMsg != nil {
		downloadable, ok := mediaMsg.(whatsmeow.DownloadableMessage)
		if ok {
			mediaBytes, err := client.Download(context.Background(), downloadable)
			if err == nil && len(mediaBytes) > 0 {
				ext := getExtension(mediaType)
				fileName := fmt.Sprintf("media_%d%s", time.Now().UnixNano(), ext)
				
				uploadedURL := uploadToCatbox(mediaBytes, fileName)
				if uploadedURL != "" {
					mediaURL = uploadedURL
				}
			}
		}
	}

	// 4. فائنل میسج سیو (نئے Quoted کالمز کے ساتھ)
	_, err := CRMDB.Exec(`
		INSERT INTO crm_messages (msg_id, bot_jid, chat_jid, sender_jid, message_text, media_url, media_type, quoted_msg_id, quoted_text, quoted_media_type, is_status, timestamp) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) 
		ON CONFLICT (msg_id) DO NOTHING
	`, msgID, botJID, chatJID, senderJID, textBody, mediaURL, mediaType, quotedMsgID, quotedText, quotedMediaType, isStatus, timestamp)

	if err != nil {
		fmt.Printf("⚠️ [CRM DATABASE] Error saving message: %v\n", err)
	} else {
		// ⚡ فلٹر ایپ کو لائیو ڈیٹا بھیجیں! (یہاں بھی کلین نمبرز جائیں گے)
		liveData := map[string]interface{}{
			"bot_jid":           botJID,
			"chat_jid":          chatJID,
			"sender_jid":        senderJID,
			"message_text":      textBody,
			"media_url":         mediaURL,
			"media_type":        mediaType,
			"quoted_msg_id":     quotedMsgID,
			"quoted_text":       quotedText,
			"quoted_media_type": quotedMediaType,
			"is_status":         isStatus,
			"timestamp":         timestamp,
		}
		BroadcastToWebsocket(liveData) 
	}
}

// ==========================================
// 🖼️ 3. CATBOX API UPLOADER
// ==========================================
func uploadToCatbox(fileBytes []byte, filename string) string {
	targetURL := "https://catbox-production-7629.up.railway.app/upload"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("reqtype", "fileupload")

	part, err := writer.CreateFormFile("fileToUpload", filename)
	if err != nil { return "" }
	_, _ = io.Copy(part, bytes.NewReader(fileBytes))
	writer.Close()

	req, err := http.NewRequest("POST", targetURL, body)
	if err != nil { return "" }
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return "" }
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return string(respBody) 
	}
	return ""
}
