package main

import (
	"context"
	"encoding/json"
	"net/http"
	"fmt"
	"strings"
    "database/sql"
	"time"
	"io"
	"os"
	"os/exec"
	
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 🛠️ SMART JID PARSER (یہ کلین نمبر کو واٹس ایپ کے قابل بنائے گا)
// ==========================================
func parseSmartJID(jidStr string) types.JID {
	if jidStr == "" {
		return types.EmptyJID
	}
	// اگر نمبر میں @ نہیں ہے تو سمجھو یہ کلین نمبر ہے، اسے فارمیٹ کرو
	if !strings.Contains(jidStr, "@") {
		if strings.Contains(jidStr, "-") {
			jidStr += "@g.us" // گروپ کے لیے
		} else if jidStr == "status" {
			jidStr += "@broadcast" // سٹیٹس کے لیے
		} else {
			jidStr += "@s.whatsapp.net" // نارمل یوزر کے لیے
		}
	}
	parsed, _ := types.ParseJID(jidStr)
	return parsed
}

// ==========================================
// 📥 1. GET CHATS LIST API
// ==========================================
func GetChatsAPI(w http.ResponseWriter, r *http.Request) {
	botJid := r.URL.Query().Get("bot_jid")
	if botJid == "" {
		http.Error(w, "bot_jid required", http.StatusBadRequest)
		return
	}

	rows, err := CRMDB.Query(`
		SELECT c.chat_jid, c.last_message, c.updated_at, ct.push_name 
		FROM crm_chats c 
		LEFT JOIN crm_contacts ct ON c.chat_jid = ct.user_jid AND c.bot_jid = ct.bot_jid 
		WHERE c.bot_jid = $1 ORDER BY c.updated_at DESC
	`, botJid)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 🔍 بوٹ کا سیشن نکالیں تاکہ DP فیچ کر سکیں
	clientsMutex.RLock()
	botClient := activeClients[botJid]
	clientsMutex.RUnlock()

	var chats []map[string]interface{}
	for rows.Next() {
		var chatJid, lastMsg string
		var updatedAt int64
		var pushName sql.NullString
		rows.Scan(&chatJid, &lastMsg, &updatedAt, &pushName)

		name := chatJid
		if pushName.Valid && pushName.String != "" {
			name = pushName.String
		}

		// 🖼️ Profile Picture Fetch Logic
		dpUrl := ""
		if botClient != nil {
			targetJid := parseSmartJID(chatJid) // 🚀 Smart Parser استعمال کیا
			
			picInfo, err := botClient.GetProfilePictureInfo(context.Background(), targetJid, &whatsmeow.GetProfilePictureParams{
				Preview: true,
			})
			if err == nil && picInfo != nil {
				dpUrl = picInfo.URL
			}
		}

		chats = append(chats, map[string]interface{}{
			"chat_jid":     chatJid, // 👈 یہ ہمیشہ کلین ہوگا کیونکہ DB میں کلین ہے
			"push_name":    name,
			"last_message": lastMsg,
			"updated_at":   updatedAt,
			"dp_url":       dpUrl, 
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chats)
}
// ==========================================
// 📥 2. GET MESSAGES API
// ==========================================

func GetMessagesAPI(w http.ResponseWriter, r *http.Request) {
	botJID := r.URL.Query().Get("bot_jid")
	chatJID := r.URL.Query().Get("chat_jid")

	if botJID == "" || chatJID == "" || CRMDB == nil {
		http.Error(w, "Missing parameters", http.StatusBadRequest)
		return
	}

	rows, err := CRMDB.Query("SELECT msg_id, sender_jid, message_text, media_url, media_type, quoted_msg_id, quoted_text, quoted_media_type, is_status, timestamp FROM crm_messages WHERE bot_jid = $1 AND chat_jid = $2 ORDER BY timestamp ASC", botJID, chatJID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []map[string]interface{}
	for rows.Next() {
		var msgID, sender, text, mediaUrl, mediaType string
		var quotedMsgID, quotedText, quotedMediaType sql.NullString 
		var isStatus bool
		var ts int64
		
		rows.Scan(&msgID, &sender, &text, &mediaUrl, &mediaType, &quotedMsgID, &quotedText, &quotedMediaType, &isStatus, &ts)
		
		messages = append(messages, map[string]interface{}{
			"msg_id":            msgID, 
			"sender_jid":        sender, // 👈 کلین جائے گا
			"text":              text,
			"media_url":         mediaUrl, 
			"media_type":        mediaType, 
			"quoted_msg_id":     quotedMsgID.String,     
			"quoted_text":       quotedText.String,      
			"quoted_media_type": quotedMediaType.String, 
			"is_status":         isStatus, 
			"timestamp":         ts,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// ==========================================
// 🚀 3. SEND MESSAGE & REPLY API
// ==========================================
type SendMsgReq struct {
	BotJID           string `json:"bot_jid"`
	ChatJID          string `json:"chat_jid"`
	Text             string `json:"text"`
	ReplyToMsgID     string `json:"reply_to_msg_id"`     
	ReplyParticipant string `json:"reply_participant"` 
}

func SendMessageAPI(w http.ResponseWriter, r *http.Request) {
	var req SendMsgReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[req.BotJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found or inactive", http.StatusNotFound)
		return
	}

	// 🚀 Smart Parser استعمال کیا تاکہ کلین نمبر واٹس ایپ سرور تک جا سکے
	targetJID := parseSmartJID(req.ChatJID)

	// میسج بلڈ کریں
	msgToSend := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String(req.Text),
		},
	}

	// 💬 اگر ریپلائی (Quote) کیا گیا ہے تو ContextInfo ایڈ کریں
	if req.ReplyToMsgID != "" && req.ReplyParticipant != "" {
		participantJID := parseSmartJID(req.ReplyParticipant) // 🚀 Smart Parser
		msgToSend.ExtendedTextMessage.ContextInfo = &waE2E.ContextInfo{
			StanzaID:    proto.String(req.ReplyToMsgID),
			Participant: proto.String(participantJID.String()),
		}
	}

	// میسج سینڈ کریں
	resp, err := client.SendMessage(context.Background(), targetJID, msgToSend)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Add message to local DB since we sent it (save.go والا فنکشن اسے خود کلین کر دے گا)
	go ProcessAndSaveMessage(client, req.BotJID, resp.ID, req.ChatJID, req.BotJID, "Me", req.Text, false, false, "", nil, resp.Timestamp.Unix(), req.ReplyToMsgID, "", "")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "msg_id": resp.ID})
}

// ==========================================
// 🗑️ 4. CLEAR BOT HISTORY API
// ==========================================
type ClearHistoryReq struct {
	BotJID string `json:"bot_jid"`
}

func ClearBotHistoryAPI(w http.ResponseWriter, r *http.Request) {
	var req ClearHistoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if CRMDB == nil {
		http.Error(w, "Database not connected", http.StatusInternalServerError)
		return
	}

	if req.BotJID == "" {
		http.Error(w, "bot_jid is required", http.StatusBadRequest)
		return
	}

	req.BotJID = strings.Split(req.BotJID, "@")[0] // 🚀 کلین کیا

	_, err1 := CRMDB.Exec("DELETE FROM crm_messages WHERE bot_jid = $1", req.BotJID)
	_, err2 := CRMDB.Exec("DELETE FROM crm_chats WHERE bot_jid = $1", req.BotJID)
	_, err3 := CRMDB.Exec("DELETE FROM crm_contacts WHERE bot_jid = $1", req.BotJID)

	if err1 != nil || err2 != nil || err3 != nil {
		fmt.Printf("⚠️ Error clearing history for %s: %v, %v, %v\n", req.BotJID, err1, err2, err3)
		http.Error(w, "Failed to clear history from database", http.StatusInternalServerError)
		return
	}

	fmt.Printf("🧹 [HISTORY CLEARED] All chat data deleted for bot: %s\n", req.BotJID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "All history cleared successfully for " + req.BotJID,
	})
}

// ==========================================
// 🔐 5. AUTH & ADMIN APIs (VIP CRM Logic)
// ==========================================

type LoginReq struct {
	Key string `json:"key"`
}

func LoginAPI(w http.ResponseWriter, r *http.Request) {
	var req LoginReq
	json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")

	if req.Key == "admin786" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success", "is_admin": true, "auto_allow": true, "allowed_bots": []string{},
		})
		return
	}

	var allowedBotsStr string
	var autoAllow, isAdmin bool
	err := CRMDB.QueryRow("SELECT allowed_bots, auto_allow, is_admin FROM crm_vip_keys WHERE key_string = $1", req.Key).Scan(&allowedBotsStr, &autoAllow, &isAdmin)

	if err == sql.ErrNoRows {
		http.Error(w, `{"status":"error", "message":"Invalid Access Key"}`, http.StatusUnauthorized)
		return
	}

	var allowedBots []string
	json.Unmarshal([]byte(allowedBotsStr), &allowedBots)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success", "is_admin": isAdmin, "auto_allow": autoAllow, "allowed_bots": allowedBots,
	})
}

func GetActiveBotsAPI(w http.ResponseWriter, r *http.Request) {
	clientsMutex.RLock()
	var bots []string
	for jid := range activeClients {
		bots = append(bots, strings.Split(jid, "@")[0]) // 🚀 کلین کیا
	}
	clientsMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bots)
}

type CreateKeyReq struct {
	Key         string   `json:"key"`
	AllowedBots []string `json:"allowed_bots"`
	AutoAllow   bool     `json:"auto_allow"`
	IsAdmin     bool     `json:"is_admin"`
}

func CreateKeyAPI(w http.ResponseWriter, r *http.Request) {
	var req CreateKeyReq
	json.NewDecoder(r.Body).Decode(&req)

	if req.AllowedBots == nil {
		req.AllowedBots = []string{}
	}

	botsJSON, _ := json.Marshal(req.AllowedBots)
	timestamp := time.Now().Unix()

	_, err := CRMDB.Exec("INSERT INTO crm_vip_keys (key_string, allowed_bots, auto_allow, is_admin, created_at) VALUES ($1, $2, $3, $4, $5)",
		req.Key, string(botsJSON), req.AutoAllow, req.IsAdmin, timestamp)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func GetKeysAPI(w http.ResponseWriter, r *http.Request) {
	rows, _ := CRMDB.Query("SELECT key_string, allowed_bots, auto_allow, is_admin FROM crm_vip_keys ORDER BY created_at DESC")
	defer rows.Close()

	var keys []map[string]interface{}
	for rows.Next() {
		var keyStr, botsStr string
		var autoAllow, isAdmin bool
		rows.Scan(&keyStr, &botsStr, &autoAllow, &isAdmin)

		var botsList []string
		json.Unmarshal([]byte(botsStr), &botsList)

		keys = append(keys, map[string]interface{}{
			"key": keyStr, "allowed_bots": botsList, "auto_allow": autoAllow, "is_admin": isAdmin,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keys)
}

type EditKeyReq struct {
	Key         string   `json:"key"`
	AllowedBots []string `json:"allowed_bots"`
	AutoAllow   bool     `json:"auto_allow"`
}

func EditKeyAPI(w http.ResponseWriter, r *http.Request) {
	var req EditKeyReq
	json.NewDecoder(r.Body).Decode(&req)

	botsJSON, _ := json.Marshal(req.AllowedBots)
	_, err := CRMDB.Exec("UPDATE crm_vip_keys SET allowed_bots = $1, auto_allow = $2 WHERE key_string = $3", string(botsJSON), req.AutoAllow, req.Key)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func DeleteKeyAPI(w http.ResponseWriter, r *http.Request) {
	keyStr := r.URL.Query().Get("key")
	CRMDB.Exec("DELETE FROM crm_vip_keys WHERE key_string = $1", keyStr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ==========================================
// 🚀 6. NEW WHATSAPP CLONE APIs
// ==========================================

type DeleteMsgReq struct {
	BotJID    string `json:"bot_jid"`
	ChatJID   string `json:"chat_jid"`
	MsgID     string `json:"msg_id"`
	Everyone  bool   `json:"everyone"`
}

func DeleteMessageAPI(w http.ResponseWriter, r *http.Request) {
	var req DeleteMsgReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[req.BotJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found", http.StatusNotFound)
		return
	}

	chatJID := parseSmartJID(req.ChatJID) // 🚀 Smart Parser
	
	if req.Everyone {
		revokeMsg := client.BuildRevoke(chatJID, types.EmptyJID, req.MsgID)
		_, err := client.SendMessage(context.Background(), chatJID, revokeMsg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	
	if CRMDB != nil {
	   CRMDB.Exec("DELETE FROM crm_messages WHERE msg_id = $1", req.MsgID)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type DeleteChatReq struct {
	BotJID  string `json:"bot_jid"`
	ChatJID string `json:"chat_jid"`
}

func DeleteChatAPI(w http.ResponseWriter, r *http.Request) {
	var req DeleteChatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[req.BotJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found", http.StatusNotFound)
		return
	}

	req.BotJID = strings.Split(req.BotJID, "@")[0]
	req.ChatJID = strings.Split(req.ChatJID, "@")[0]

	if CRMDB != nil {
	   CRMDB.Exec("DELETE FROM crm_chats WHERE bot_jid = $1 AND chat_jid = $2", req.BotJID, req.ChatJID)
	   CRMDB.Exec("DELETE FROM crm_messages WHERE bot_jid = $1 AND chat_jid = $2", req.BotJID, req.ChatJID)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type ReactionReq struct {
	BotJID  string `json:"bot_jid"`
	ChatJID string `json:"chat_jid"`
	MsgID   string `json:"msg_id"`
	Emoji   string `json:"emoji"`
}

func SendReactionAPI(w http.ResponseWriter, r *http.Request) {
	var req ReactionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[req.BotJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found", http.StatusNotFound)
		return
	}

	chatJID := parseSmartJID(req.ChatJID) // 🚀 Smart Parser
	
	msg := client.BuildReaction(chatJID, types.EmptyJID, req.MsgID, req.Emoji)
	_, err := client.SendMessage(context.Background(), chatJID, msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type BlockUserReq struct {
	BotJID  string `json:"bot_jid"`
	UserJID string `json:"user_jid"`
	Block   bool   `json:"block"`
}

func BlockUserAPI(w http.ResponseWriter, r *http.Request) {
	var req BlockUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[req.BotJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found", http.StatusNotFound)
		return
	}

	userJID := parseSmartJID(req.UserJID) // 🚀 Smart Parser
	
	var err error
	if req.Block {
		_, err = client.UpdateBlocklist(context.Background(), userJID, events.BlocklistChangeActionBlock)
	} else {
		_, err = client.UpdateBlocklist(context.Background(), userJID, events.BlocklistChangeActionUnblock)
	}
	
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func SendMediaAPI(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	botJID := r.FormValue("bot_jid")
	chatJIDStr := r.FormValue("chat_jid")
	mediaType := r.FormValue("media_type")
	replyToMsgId := r.FormValue("reply_to_msg_id")
	replyParticipant := r.FormValue("reply_participant")

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusInternalServerError)
		return
	}

	clientsMutex.RLock()
	client, exists := activeClients[botJID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot session not found", http.StatusNotFound)
		return
	}

	chatJID := parseSmartJID(chatJIDStr) // 🚀 Smart Parser

	// 🔥 VIP JUGAAD: بالکل .toptt کمانڈ والی FFmpeg کنورژن لاجک!
	// 🔥 VIP JUGAAD: بالکل .toptt کمانڈ والی FFmpeg کنورژن لاجک!
	if mediaType == "audio" {
		tempIn := fmt.Sprintf("./data/api_audio_in_%d.tmp", time.Now().UnixNano())
		tempOut := fmt.Sprintf("./data/api_audio_out_%d.ogg", time.Now().UnixNano())

		// فرنٹ اینڈ سے آنے والی کچی آڈیو کو لکھیں
		os.WriteFile(tempIn, fileBytes, 0644)
		
		// 🚀 100% WHATSAPP PTT FIX: -ac 1 (Mono) اور -ar 24000 کا اضافہ کر دیا گیا ہے
		cmdErr := exec.Command("ffmpeg", "-y", "-i", tempIn, 
			"-c:a", "libopus", 
			"-b:a", "32k", 
			"-vbr", "on", 
			"-compression_level", "10", 
			"-frame_duration", "20", 
			"-application", "voip", 
			"-ac", "1",           // 👈 یہ سب سے اہم ہے! (سٹیریو کو مونو میں بدل دے گا)
			"-ar", "24000",       // 👈 واٹس ایپ کا سٹینڈرڈ سیمپل ریٹ
			tempOut).Run()
		
		if cmdErr == nil {
			convertedBytes, readErr := os.ReadFile(tempOut)
			if readErr == nil && len(convertedBytes) > 0 {
				fileBytes = convertedBytes // 👈 پرانی آڈیو کی جگہ واٹس ایپ والی آڈیو رکھ دی
				fmt.Println("✅ API Audio successfully converted to WhatsApp Mono PTT!")
			}
		} else {
			fmt.Printf("⚠️ FFmpeg conversion failed for API audio: %v\n", cmdErr)
		}
		
		// فائلز ڈیلیٹ کرنا نہ بھولیں تاکہ سرور نہ بھرے
		os.Remove(tempIn)
		os.Remove(tempOut) 
	}

	// 1. Upload media to WhatsApp server
	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		waMediaType = whatsmeow.MediaDocument
	}

	resp, err := client.Upload(context.Background(), fileBytes, waMediaType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload media: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Build the message
	msgToSend := &waE2E.Message{}

	switch mediaType {
	case "image":
		msgToSend.ImageMessage = &waE2E.ImageMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String(http.DetectContentType(fileBytes)),
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(fileBytes))),
		}
	case "video":
		msgToSend.VideoMessage = &waE2E.VideoMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String(http.DetectContentType(fileBytes)),
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(fileBytes))),
		}
	case "audio":
		msgToSend.AudioMessage = &waE2E.AudioMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(fileBytes))),
			PTT:           proto.Bool(true), // 👈 اب یہ چلے گی!
		}
	default:
		msgToSend.DocumentMessage = &waE2E.DocumentMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String(http.DetectContentType(fileBytes)),
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(fileBytes))),
			FileName:      proto.String(handler.Filename),
		}
	}

	// 3. Add Reply Context if present
	if replyToMsgId != "" && replyParticipant != "" {
		participantJID := parseSmartJID(replyParticipant) 
		contextInfo := &waE2E.ContextInfo{
			StanzaID:    proto.String(replyToMsgId),
			Participant: proto.String(participantJID.String()),
		}

		if msgToSend.ImageMessage != nil {
			msgToSend.ImageMessage.ContextInfo = contextInfo
		} else if msgToSend.VideoMessage != nil {
			msgToSend.VideoMessage.ContextInfo = contextInfo
		} else if msgToSend.AudioMessage != nil {
			msgToSend.AudioMessage.ContextInfo = contextInfo
		} else if msgToSend.DocumentMessage != nil {
			msgToSend.DocumentMessage.ContextInfo = contextInfo
		}
	}

	// 4. Send the message
	sendResp, err := client.SendMessage(context.Background(), chatJID, msgToSend)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to send message: %v", err), http.StatusInternalServerError)
		return
	}

	// 5. Save to local DB natively
	go ProcessAndSaveMessage(client, botJID, sendResp.ID, chatJIDStr, botJID, "Me", "", false, true, mediaType, nil, sendResp.Timestamp.Unix(), replyToMsgId, "", "")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "msg_id": sendResp.ID})
}


func getExtension(mediaType string) string {
	switch mediaType {
	case "image": return ".jpg"
	case "video": return ".mp4"
	case "audio": return ".ogg" 
	case "document": return ".bin"
	case "sticker": return ".webp" 
	default: return ".dat"
	}
}
