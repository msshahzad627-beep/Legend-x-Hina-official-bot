package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

// ==========================================
// 🛡️ STATE CACHES & VARIABLES FOR AI
// ==========================================
type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AISession struct {
	SenderID string
	Messages []AIMessage
	BotLID   string
}

// یہ میپ تمام یوزرز کی چیٹ ہسٹری محفوظ رکھے گا
var aiCache = make(map[string]AISession)

// نیا گلوبل ویری ایبل جو آپ کا کسٹم پرومپٹ محفوظ رکھے گا
var dynamicAIPrompt string = ""

// ==========================================
// 🧠 AI COMMAND ROUTER
// ==========================================
func handleAICommand(client *whatsmeow.Client, v *events.Message, query string, cmd string) {
	if query == "" {
		replyMessage(client, v, "❌ *Error:* Please ask a question.\nExample: `.ai Hello kia hal hai?`\n\n*To set custom mode:* `.ai set <your prompt>`\n*To reset mode:* `.ai reset`")
		return
	}

	// 1️⃣ کمانڈ چیک کریں: کیا یوزر نیا پرومپٹ سیٹ کر رہا ہے؟
	if strings.HasPrefix(strings.ToLower(query), "set ") {
		newPrompt := strings.TrimSpace(query[4:])
		if newPrompt == "" {
			replyMessage(client, v, "❌ *Error:* Please provide a prompt.\nExample: `.ai set Act like a strict math teacher.`")
			return
		}
		dynamicAIPrompt = newPrompt
		replyMessage(client, v, "✅ *AI Mode Updated Successfully!*\n\n*New Persona:* "+dynamicAIPrompt)
		return
	}

	// 2️⃣ کمانڈ چیک کریں: کیا یوزر پرومپٹ ری سیٹ کر رہا ہے؟
	if strings.ToLower(strings.TrimSpace(query)) == "reset" {
		dynamicAIPrompt = ""
		replyMessage(client, v, "🔄 *AI Mode Reset to Default!*")
		return
	}

	react(client, v, "🧠")

	// 3️⃣ بیس رولز جو کبھی تبدیل نہیں ہوں گے (یہ ہر موڈ کے ساتھ چپکے رہیں گے)
	baseRules := `
STRICT SYSTEM RULES (FOLLOW THESE NO MATTER YOUR PERSONA):
1. MATCH LENGTH EXACTLY: If the user writes a single line, YOU MUST reply with a single line. NEVER write long paragraphs or multi-point explanations unless explicitly requested by the user. Keep it natural and concise.
2. LANGUAGE MIRRORING: Always reply in the exact language and script the user uses (Roman Urdu -> Roman Urdu, Pure Urdu -> Pure Urdu, English -> English).
3. NO REPETITION: Do not simply echo or copy-paste what the user says. Add value to the conversation.`

	// 4️⃣ فائنل پرومپٹ تیار کرنا
	var persona string
	if dynamicAIPrompt != "" {
		// اگر آپ نے کوئی موڈ سیٹ کیا ہوا ہے
		persona = "You must act strictly under the following persona:\n" + dynamicAIPrompt + "\n\n" + baseRules
	} else {
		// ڈیفالٹ نارمل موڈ
		persona = "You are a friendly, intelligent, and helpful AI assistant. Answer questions clearly but briefly.\n\n" + baseRules
	}

	switch cmd {
	case "gpt", "chatgpt", "gemini", "claude", "llama", "groq":
		// خاموشی سے اوپر والا پرومپٹ چلنے دیں
	default:
	}

	session := AISession{
		SenderID: v.Info.Sender.User, 
		BotLID:   getCleanID(client.Store.ID.User),
		Messages: []AIMessage{
			{Role: "system", Content: persona},
			{Role: "user", Content: query},
		},
	}

	go processAndSendAI(client, v, session)
}

func processAndSendAI(client *whatsmeow.Client, v *events.Message, session AISession) {
	react(client, v, "⏳")

	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("❌ [AI ERROR] GROQ_API_KEY is missing in Environment Variables!")
		replyMessage(client, v, "❌ System Error: API Key is missing. Developer ko batao!")
		react(client, v, "❌")
		return
	}

	requestBody := map[string]interface{}{
		"model":       "llama-3.3-70b-versatile",
		"messages":    session.Messages,
		"temperature": 0.7, // نارمل اور سلجھے ہوئے جوابات کے لیے
		"max_tokens":  250, // لمبی کہانیاں روکنے کے لیے کنٹرول
		"top_p":       0.9,
	}

	jsonData, _ := json.Marshal(requestBody)
	req, _ := http.NewRequest("POST", "https://api.groq.com/openai/v1/chat/completions", bytes.NewBuffer(jsonData))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)

	if err != nil {
		fmt.Printf("❌ [AI NETWORK ERROR]: %v\n", err)
		replyMessage(client, v, "❌ Network issue while connecting to AI Engine.")
		react(client, v, "❌")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errorBody, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ [GROQ API ERROR] Status: %d\nResponse: %s\n", resp.StatusCode, string(errorBody))
		replyMessage(client, v, "❌ AI Engine is currently resting or busy. Check console logs.")
		react(client, v, "❌")
		return
	}

	var groqResp struct {
		Choices []struct {
			Message AIMessage `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&groqResp)

	if len(groqResp.Choices) > 0 {
		aiReplyText := groqResp.Choices[0].Message.Content

		msgID := replyMessage(client, v, aiReplyText)

		session.Messages = append(session.Messages, AIMessage{Role: "assistant", Content: aiReplyText})

		if msgID != "" {
			aiCache[msgID] = session

			go func(id string) {
				time.Sleep(1 * time.Hour)
				delete(aiCache, id)
			}(msgID)
		}

		react(client, v, "✅")
	} else {
		replyMessage(client, v, "❌ Got an empty response from AI.")
		react(client, v, "❌")
	}
}

// ==========================================
// 🔄 INTERCEPTOR FOR AI REPLIES (GROUP MULTI-USER)
// ==========================================
func HandleAIChatReply(client *whatsmeow.Client, v *events.Message, bodyClean string, qID string) bool {
	if session, ok := aiCache[qID]; ok {
		delete(aiCache, qID) // پرانی آئی ڈی ڈیلیٹ کریں
		
		session.Messages = append(session.Messages, AIMessage{Role: "user", Content: bodyClean})
		
		if len(session.Messages) > 15 {
			session.Messages = append([]AIMessage{session.Messages[0]}, session.Messages[len(session.Messages)-14:]...)
		}

		go processAndSendAI(client, v, session)
		return true
	}
	return false
}

// ==========================================
// 🛠️ UTILITY: ID CLEANER
// ==========================================
func getCleanID(jidStr string) string {
	if jidStr == "" { return "unknown" }
	parts := strings.Split(jidStr, "@")
	if len(parts) == 0 { return "unknown" }
	userPart := parts[0]
	if strings.Contains(userPart, ":") {
		userPart = strings.Split(userPart, ":")[0]
	}
	if strings.Contains(userPart, ".") {
		userPart = strings.Split(userPart, ".")[0]
	}
	return strings.TrimSpace(userPart)
}
