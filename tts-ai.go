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
		replyMessage(client, v, "❌ *Usage:* `.tts Hello, how are you?`\n*With custom voice:* `.tts am_adam|Hello bro`\n\n*Available Voices:* af_heart, af_bella, am_adam, am_michael, female, male")
		return
	}

	// 1. ڈیفالٹ سیٹنگز
	targetVoice := "af_heart"
	textToSpeak := args

	// اگر یوزر نے آواز کا نام الگ سے دیا ہو (مثال: .tts am_adam|Hello)
	if strings.Contains(args, "|") {
		parts := strings.SplitN(args, "|", 2)
		if len(parts) == 2 {
			targetVoice = strings.TrimSpace(parts[0])
			textToSpeak = strings.TrimSpace(parts[1])
		}
	} else {
		// Simple voice shortcut: .tts female Hello / .tts male Hello
		words := strings.Fields(args)
		if len(words) > 1 {
			switch strings.ToLower(words[0]) {
			case "female":
				targetVoice = "af_heart"
				textToSpeak = strings.Join(words[1:], " ")
			case "male":
				targetVoice = "am_adam"
				textToSpeak = strings.Join(words[1:], " ")
			}
		}
	}

	react(client, v, "🎙️")

	// ==========================================
	// 🔥 SMART TTS ENGINE - Railway + Fallback
	// ==========================================
	var mp3Data []byte
	var fetchErr error

	// --- TIER 1: Railway Internal (sabse fast) ---
	internalAPI := "http://kokoro-fastapi-cpu.railway.internal:8880/v1/audio/speech"
	requestPayload := KokoroInternalRequest{
		Text:  textToSpeak,
		Voice: targetVoice,
		Speed: 1.0,
	}
	jsonData, _ := json.Marshal(requestPayload)

	railwayClient := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("POST", internalAPI, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := railwayClient.Do(req)
	if err == nil && resp.StatusCode == 200 {
		mp3Data, fetchErr = io.ReadAll(resp.Body)
		resp.Body.Close()
		if fetchErr != nil || len(mp3Data) < 1000 {
			mp3Data = nil // invalid response, fallback jaao
		}
	} else {
		if resp != nil { resp.Body.Close() }
		fmt.Printf("⚠️ [TTS] Railway internal offline (%v), trying fallback...\n", err)
	}

	// --- TIER 2: Streamelements Free TTS (public fallback) ---
	if mp3Data == nil {
		// Voice mapping: kokoro voices -> streamelements voices
		seVoice := "Brian" // default male
		switch strings.ToLower(targetVoice) {
		case "af_heart", "af_bella", "female":
			seVoice = "Amy"
		case "am_adam", "am_michael", "male":
			seVoice = "Brian"
		}

		seURL := fmt.Sprintf("https://api.streamelements.com/kappa/v2/speech?voice=%s&text=%s",
			seVoice, strings.ReplaceAll(textToSpeak, " ", "+"))

		seClient := &http.Client{Timeout: 15 * time.Second}
		seResp, seErr := seClient.Get(seURL)
		if seErr == nil && seResp.StatusCode == 200 {
			mp3Data, fetchErr = io.ReadAll(seResp.Body)
			seResp.Body.Close()
			if fetchErr != nil || len(mp3Data) < 500 {
				mp3Data = nil
			} else {
				fmt.Printf("✅ [TTS] Streamelements fallback successful!\n")
			}
		} else {
			if seResp != nil { seResp.Body.Close() }
			fmt.Printf("❌ [TTS] Streamelements also failed: %v\n", seErr)
		}
	}

	// --- TIER 3: Google TTS (last resort) ---
	if mp3Data == nil {
		gttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&q=%s&tl=en&client=tw-ob",
			strings.ReplaceAll(textToSpeak, " ", "+"))
		gttsClient := &http.Client{Timeout: 15 * time.Second}
		gttsReq, _ := http.NewRequest("GET", gttsURL, nil)
		gttsReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

		gttsResp, gttsErr := gttsClient.Do(gttsReq)
		if gttsErr == nil && gttsResp.StatusCode == 200 {
			mp3Data, _ = io.ReadAll(gttsResp.Body)
			gttsResp.Body.Close()
			fmt.Printf("✅ [TTS] Google TTS fallback successful!\n")
		} else {
			if gttsResp != nil { gttsResp.Body.Close() }
		}
	}

	// Agar teeno fail ho gaye
	if len(mp3Data) < 500 {
		replyMessage(client, v, "❌ *TTS service abhi available nahi hai.*\n💡 `.gtts` command try karo:\n`.gtts `"+textToSpeak)
		react(client, v, "❌")
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

	// 5. واٹس ایپ سرور پر اپلوڈ کریں
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
		fmt.Printf("❌ [SEND TTS FAILED]: %v\n", err)
	} else {
		react(client, v, "✅")
	}
}

// ==========================================
// 🌐 GOOGLE TTS (Free - Urdu/Hindi/English)
// ==========================================
func handleGoogleTTS(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Usage:*\n`.gtts Hello how are you` — English\n`.gtts ur Assalam o Alaikum` — Urdu\n`.gtts hi Kya haal hai` — Hindi")
		return
	}
	react(client, v, "🎙️")

	lang := "en"
	textToSpeak := args
	parts := strings.Fields(args)
	if len(parts) > 1 {
		switch strings.ToLower(parts[0]) {
		case "ur", "urdu":
			lang = "ur"
			textToSpeak = strings.Join(parts[1:], " ")
		case "hi", "hindi":
			lang = "hi"
			textToSpeak = strings.Join(parts[1:], " ")
		case "en", "english":
			lang = "en"
			textToSpeak = strings.Join(parts[1:], " ")
		}
	}

	ttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&q=%s&tl=%s&client=tw-ob",
		strings.ReplaceAll(textToSpeak, " ", "+"), lang)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", ttsURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ Google TTS failed. Try again!")
		react(client, v, "❌")
		return
	}
	defer resp.Body.Close()

	mp3Data, err := io.ReadAll(resp.Body)
	if err != nil || len(mp3Data) < 500 {
		replyMessage(client, v, "❌ Audio not received from Google.")
		return
	}

	timestamp := time.Now().UnixNano()
	tempIn := fmt.Sprintf("./data/gtts_in_%d.mp3", timestamp)
	tempOut := fmt.Sprintf("./data/gtts_out_%d.ogg", timestamp)

	_ = os.WriteFile(tempIn, mp3Data, 0644)
	defer func() { os.Remove(tempIn); os.Remove(tempOut) }()

	err = exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", tempOut).Run()
	if err != nil {
		replyMessage(client, v, "❌ Audio conversion failed. Is ffmpeg installed?")
		return
	}

	oggData, err := os.ReadFile(tempOut)
	if err != nil || len(oggData) == 0 {
		replyMessage(client, v, "❌ Converted audio is empty.")
		return
	}

	up, err := client.Upload(context.Background(), oggData, whatsmeow.MediaAudio)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp upload failed.")
		return
	}

	isVoiceNote := true
	_, err = client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(oggData))), PTT: &isVoiceNote,
			ContextInfo: &waProto.ContextInfo{
				StanzaID: proto.String(v.Info.ID), Participant: proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
	if err != nil {
		react(client, v, "❌")
	} else {
		react(client, v, "✅")
	}
}
