package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Kokoro FastAPI پے لوڈ اسٹرکچر
type KokoroInternalRequest struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice"` // ڈیفالٹ پریمیم آوازیں: af_heart, am_adam, af_bella وغیرہ
	Speed float64 `json:"speed"`
}

func handleAdvancedTTS(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Usage:* `.speak Hello, how are you?`\n*With custom voice:* `.speak am_adam|Hello bro`\n\n*Available Voices:* af_heart, af_bella, am_adam, am_michael")
		return
	}

	// 1. ڈیفالٹ سیٹنگز
	targetVoice := "af_heart" // سب سے بہترین ہیسٹ ہیومن آواز
	textToSpeak := args

	// اگر یوزر نے آواز کا نام الگ سے دیا ہو (مثال: .speak am_adam|Hello)
	if strings.Contains(args, "|") {
		parts := strings.SplitN(args, "|", 2)
		if len(parts) == 2 {
			targetVoice = strings.TrimSpace(parts[0])
			textToSpeak = strings.TrimSpace(parts[1])
		}
	}

	react(client, v, "🎙️")

	// 🚀 ریلوے کا انٹرنل پرائیویٹ یو آر ایل پورٹ 8880 کے ساتھ (FastAPI کا ڈیفالٹ پورٹ)
	internalAPI := "http://kokoro-fastapi-cpu.railway.internal:8880/v1/audio/speech"

	requestPayload := KokoroInternalRequest{
		Text:  textToSpeak,
		Voice: targetVoice,
		Speed: 1.0,
	}

	jsonData, err := json.Marshal(requestPayload)
	if err != nil {
		replyMessage(client, v, "❌ Failed to parse JSON payload.")
		return
	}

	req, _ := http.NewRequest("POST", internalAPI, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	// انٹرنل نیٹ ورک ہونے کی وجہ سے 15 سیکنڈ کا ٹائم آؤٹ کافی ہے
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	
	if err != nil {
		fmt.Printf("❌ [KOKORO INTERNAL NETWORK ERROR]: %v\n", err)
		replyMessage(client, v, "❌ Internal AI service is offline or building. Please wait a moment.")
		react(client, v, "❌")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errorBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ [KOKORO API ERROR] Status: %d, Resp: %s\n", resp.StatusCode, string(errorBytes))
		replyMessage(client, v, "❌ AI Engine rejected the speech request or voice code is invalid.")
		react(client, v, "❌")
		return
	}

	// 2. آڈیو بائٹس پڑھیں
	mp3Data, err := io.ReadAll(resp.Body)
	if err != nil || len(mp3Data) < 1000 {
		replyMessage(client, v, "❌ Failed to read valid audio from AI Engine.")
		return
	}

	// 3. عارضی فائل نیمز (UnixNano تاکہ ملٹی تھریڈنگ میں فائلیں مکس نہ ہوں)
	timestamp := time.Now().UnixNano()
	tempIn := fmt.Sprintf("./data/kk_in_%d.mp3", timestamp)
	tempOut := fmt.Sprintf("./data/kk_out_%d.ogg", timestamp)

	_ = os.WriteFile(tempIn, mp3Data, 0644)
	defer func() {
		os.Remove(tempIn)
		os.Remove(tempOut)
	}()

	// 🎚️ 4. واٹس ایپ سیکیور اوپس (Opus PTT) کنورژن
	// یہ کمانڈ سی پی یو پر سیکنڈ کے دسویں حصے میں رن ہو جائے گی
	err = exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", "-compression_level", "10", tempOut).Run()
	if err != nil {
		fmt.Printf("❌ [FFMPEG KOKORO CONVERT ERROR]: %v\n", err)
		replyMessage(client, v, "❌ Graphics/Audio engine failed to pack voice note.")
		return
	}

	oggData, err := os.ReadFile(tempOut)
	if err != nil || len(oggData) == 0 {
		replyMessage(client, v, "❌ Failed to read converted audio file.")
		return
	}

	// 5. واٹس ایپ سرور پر اپلوڈ کریں (اور ہاں، context.Background یہاں بھی موجود ہے!)
	up, err := client.Upload(context.Background(), oggData, whatsmeow.MediaAudio)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp rejected the audio upload.")
		return
	}

	// 6. بطور آفیشل پش ٹو ٹاک (PTT - وائس نوٹ) سینڈ کریں
	isVoiceNote := true
	_, err = client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(oggData))),
			PTT:           &isVoiceNote,
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})

	if err != nil {
		fmt.Printf("❌ [SEND KOKORO FAILED]: %v\n", err)
	} else {
		react(client, v, "✅")
	}
}

// ==========================================
// 🌐 GOOGLE TTS FALLBACK (Free & Always Online)
// ==========================================
func handleGoogleTTS(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Usage:* `.gtts Hello kia hal hai`")
		return
	}

	react(client, v, "🎙️")

	// 1. Language detect - Urdu ya English
	lang := "en"
	textToSpeak := args
	
	if strings.HasPrefix(strings.ToLower(args), "ur ") {
		lang = "ur"
		textToSpeak = strings.TrimPrefix(args, "ur ")
		textToSpeak = strings.TrimPrefix(textToSpeak, "UR ")
	} else if strings.HasPrefix(strings.ToLower(args), "hi ") {
		lang = "hi"
		textToSpeak = args[3:]
	}

	// 2. Google TTS URL
	ttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&q=%s&tl=%s&client=tw-ob",
		strings.ReplaceAll(textToSpeak, " ", "+"), lang)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", ttsURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	
	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ Google TTS engine failed. Try again!")
		react(client, v, "❌")
		return
	}
	defer resp.Body.Close()

	mp3Data, err := io.ReadAll(resp.Body)
	if err != nil || len(mp3Data) < 500 {
		replyMessage(client, v, "❌ Failed to get audio from Google.")
		return
	}

	// 3. Convert MP3 to OGG Opus for WhatsApp
	timestamp := time.Now().UnixNano()
	tempIn := fmt.Sprintf("./data/gtts_in_%d.mp3", timestamp)
	tempOut := fmt.Sprintf("./data/gtts_out_%d.ogg", timestamp)

	_ = os.WriteFile(tempIn, mp3Data, 0644)
	defer func() {
		os.Remove(tempIn)
		os.Remove(tempOut)
	}()

	err = exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", tempOut).Run()
	if err != nil {
		replyMessage(client, v, "❌ Audio conversion failed.")
		return
	}

	oggData, err := os.ReadFile(tempOut)
	if err != nil || len(oggData) == 0 {
		replyMessage(client, v, "❌ Converted audio file is empty.")
		return
	}

	// 4. Upload to WhatsApp
	up, err := client.Upload(context.Background(), oggData, whatsmeow.MediaAudio)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp rejected the audio upload.")
		return
	}

	// 5. Send as Voice Note
	isVoiceNote := true
	_, err = client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(oggData))),
			PTT:           &isVoiceNote,
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})

	if err != nil {
		fmt.Printf("❌ [GTTS SEND FAILED]: %v\n", err)
		react(client, v, "❌")
	} else {
		react(client, v, "✅")
	}
}
