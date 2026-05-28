package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

// Kokoro FastAPI payload
type KokoroInternalRequest struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
}

// ==========================================
// 🎤 ELEVENLABS VOICE CLONING SYSTEM
// ==========================================

// ElevenLabs API Key — apni key yahan lagao
// Free account: https://elevenlabs.io (10,000 chars/month free)
const ElevenLabsAPIKey = "sk_0f5e0044a10b3267a3ea97a89035e4a"

// Cloned voice IDs in-memory (bot number → ElevenLabs voice ID)
var clonedVoices = make(map[string]string)

// ==========================================
// 🎙️ COMMAND: .vcset
// Kisi ki voice note ko reply karo → .vcset
// Bot us awaaz ko clone kar lega
// ==========================================
func handleVoiceCloneSet(client *whatsmeow.Client, v *events.Message) {
	if ElevenLabsAPIKey == "sk_0f5e0044a10b3267a3ea97a89035e4ac907ba357217469a8" {
		replyMessage(client, v, "❌ *ElevenLabs API Key set nahi hai!*\n\n📋 *Steps:*\n1. elevenlabs.io par free account banao\n2. Profile → API Keys → Copy\n3. `tts-ai.go` mein `ElevenLabsAPIKey` mein apni key lagao\n4. Redeploy karo")
		return
	}

	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		replyMessage(client, v, "❌ *Kisi voice note ko reply karo!*\n\n💡 *Usage:*\n1. Kisi ki voice note reply karo\n2. `.vcset` type karo\n3. Bot awaaz clone kar lega — phir `.tts` se usi awaaz mein bolega!")
		return
	}

	quoted := extMsg.ContextInfo.QuotedMessage
	audioMsg := quoted.GetAudioMessage()
	if audioMsg == nil {
		replyMessage(client, v, "❌ *Sirf voice note / audio reply karo!*")
		return
	}

	react(client, v, "⏳")
	replyMessage(client, v, "🎙️ *Voice clone ho rahi hai...*\nThoda wait karo (10-20 sec)!")

	// Audio download
	audioData, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Audio download failed: %v", err))
		return
	}

	// Temp files
	timestamp := time.Now().UnixNano()
	tempOgg := fmt.Sprintf("./data/vc_in_%d.ogg", timestamp)
	tempMp3 := fmt.Sprintf("./data/vc_in_%d.mp3", timestamp)
	defer func() {
		os.Remove(tempOgg)
		os.Remove(tempMp3)
	}()

	os.WriteFile(tempOgg, audioData, 0644)

	// OGG → MP3 convert (ElevenLabs MP3 chahta hai)
	err = exec.Command("ffmpeg", "-y", "-i", tempOgg, "-b:a", "128k", tempMp3).Run()
	if err != nil {
		replyMessage(client, v, "❌ Audio conversion failed.")
		return
	}

	mp3Data, err := os.ReadFile(tempMp3)
	if err != nil || len(mp3Data) == 0 {
		replyMessage(client, v, "❌ Failed to read converted audio.")
		return
	}

	// Multipart form data build
	botID := client.Store.ID.ToNonAD().User
	voiceName := fmt.Sprintf("WABot_%s", botID)

	var body bytes.Buffer
	mpWriter := multipart.NewWriter(&body)
	mpWriter.WriteField("name", voiceName)
	mpWriter.WriteField("description", "WhatsApp Bot Cloned Voice")
	filePart, _ := mpWriter.CreateFormFile("files", "voice.mp3")
	filePart.Write(mp3Data)
	mpWriter.Close()

	// ElevenLabs API call
	req, _ := http.NewRequest("POST", "https://api.elevenlabs.io/v1/voices/add", &body)
	req.Header.Set("xi-api-key", ElevenLabsAPIKey)
	req.Header.Set("Content-Type", mpWriter.FormDataContentType())

	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ ElevenLabs API error: %v", err))
		return
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		replyMessage(client, v, fmt.Sprintf("❌ ElevenLabs rejected: %s", string(respBytes)))
		return
	}

	var cloneResp map[string]interface{}
	json.Unmarshal(respBytes, &cloneResp)
	voiceID, ok := cloneResp["voice_id"].(string)
	if !ok || voiceID == "" {
		replyMessage(client, v, "❌ Voice ID nahi mila.")
		return
	}

	// Memory mein save karo
	clonedVoices[botID] = voiceID
	fmt.Printf("✅ [VCSET] Cloned! Bot: %s → VoiceID: %s\n", botID, voiceID)

	react(client, v, "✅")
	replyMessage(client, v, fmt.Sprintf("✅ *Voice successfully clone ho gayi!*\n\n🎙️ *Voice ID:* `%s`\n\n💡 Ab `.tts Assalam o Alaikum kya haal hai` likho — bilkul usi awaaz mein bolega! 🔥", voiceID))
}

// ==========================================
// 🎤 ElevenLabs TTS helper function
// ==========================================
func elevenLabsTTS(text string, voiceID string) ([]byte, error) {
	payload := map[string]interface{}{
		"text":     text,
		"model_id": "eleven_multilingual_v2",
		"voice_settings": map[string]interface{}{
			"stability":         0.5,
			"similarity_boost":  0.85,
			"style":             0.3,
			"use_speaker_boost": true,
		},
	}

	jsonData, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", voiceID)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("xi-api-key", ElevenLabsAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(errBytes))
	}

	return io.ReadAll(resp.Body)
}

// ==========================================
// 🔊 MAIN TTS HANDLER (.tts command)
// Priority: ElevenLabs Cloned → Railway → Streamelements → Google
// ==========================================
func handleAdvancedTTS(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Usage:*\n`.tts Hello kya haal hai`\n`.tts female|Hello` — female voice\n`.tts male|Hello` — male voice\n\n🎙️ *Voice clone karna hai?*\nKisi ki voice note reply karo aur `.vcset` likho!")
		return
	}

	targetVoice := "af_heart"
	textToSpeak := args

	if strings.Contains(args, "|") {
		parts := strings.SplitN(args, "|", 2)
		if len(parts) == 2 {
			targetVoice = strings.TrimSpace(parts[0])
			textToSpeak = strings.TrimSpace(parts[1])
		}
	} else {
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

	var mp3Data []byte
	botID := client.Store.ID.ToNonAD().User

	// ==========================================
	// TIER 1: ElevenLabs Cloned Voice (BEST - Real Human)
	// ==========================================
	if voiceID, exists := clonedVoices[botID]; exists && ElevenLabsAPIKey != "sk_0f5e0044a10b3267a3ea97a89035e4a" {
		fmt.Printf("🎤 [TTS] Using ElevenLabs cloned voice: %s\n", voiceID)
		data, err := elevenLabsTTS(textToSpeak, voiceID)
		if err == nil && len(data) > 500 {
			mp3Data = data
			fmt.Printf("✅ [TTS] ElevenLabs success!\n")
		} else {
			fmt.Printf("⚠️ [TTS] ElevenLabs failed (%v), trying fallback...\n", err)
		}
	}

	// ==========================================
	// TIER 2: Railway Kokoro Internal
	// ==========================================
	if mp3Data == nil {
		internalAPI := "http://kokoro-fastapi-cpu.railway.internal:8880/v1/audio/speech"
		requestPayload := KokoroInternalRequest{Text: textToSpeak, Voice: targetVoice, Speed: 1.0}
		jsonData, _ := json.Marshal(requestPayload)

		railwayClient := &http.Client{Timeout: 10 * time.Second}
		req, _ := http.NewRequest("POST", internalAPI, bytes.NewBuffer(jsonData))
		req.Header.Set("Content-Type", "application/json")

		resp, err := railwayClient.Do(req)
		if err == nil && resp.StatusCode == 200 {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && len(data) > 1000 {
				mp3Data = data
				fmt.Printf("✅ [TTS] Railway Kokoro success!\n")
			}
		} else {
			if resp != nil { resp.Body.Close() }
			fmt.Printf("⚠️ [TTS] Railway offline, trying Streamelements...\n")
		}
	}

	// ==========================================
	// TIER 3: Streamelements Free TTS
	// ==========================================
	if mp3Data == nil {
		seVoice := "Brian"
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
			data, _ := io.ReadAll(seResp.Body)
			seResp.Body.Close()
			if len(data) > 500 {
				mp3Data = data
				fmt.Printf("✅ [TTS] Streamelements success!\n")
			}
		} else {
			if seResp != nil { seResp.Body.Close() }
		}
	}

	// ==========================================
	// TIER 4: Google TTS (Last resort)
	// ==========================================
	if mp3Data == nil {
		gttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&q=%s&tl=en&client=tw-ob",
			strings.ReplaceAll(textToSpeak, " ", "+"))
		gttsClient := &http.Client{Timeout: 15 * time.Second}
		gttsReq, _ := http.NewRequest("GET", gttsURL, nil)
		gttsReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

		gttsResp, gttsErr := gttsClient.Do(gttsReq)
		if gttsErr == nil && gttsResp.StatusCode == 200 {
			data, _ := io.ReadAll(gttsResp.Body)
			gttsResp.Body.Close()
			if len(data) > 500 {
				mp3Data = data
				fmt.Printf("✅ [TTS] Google TTS fallback success!\n")
			}
		} else {
			if gttsResp != nil { gttsResp.Body.Close() }
		}
	}

	if len(mp3Data) < 500 {
		replyMessage(client, v, "❌ *TTS abhi available nahi.*\n💡 `.gtts "+textToSpeak+"` try karo!")
		react(client, v, "❌")
		return
	}

	// FFmpeg se OGG convert
	timestamp := time.Now().UnixNano()
	tempIn := fmt.Sprintf("./data/tts_in_%d.mp3", timestamp)
	tempOut := fmt.Sprintf("./data/tts_out_%d.ogg", timestamp)

	os.WriteFile(tempIn, mp3Data, 0644)
	defer func() { os.Remove(tempIn); os.Remove(tempOut) }()

	err := exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", "-compression_level", "10", tempOut).Run()
	if err != nil {
		replyMessage(client, v, "❌ Audio engine failed.")
		return
	}

	oggData, err := os.ReadFile(tempOut)
	if err != nil || len(oggData) == 0 {
		replyMessage(client, v, "❌ Converted audio empty.")
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
		fmt.Printf("❌ [TTS SEND FAILED]: %v\n", err)
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

	os.WriteFile(tempIn, mp3Data, 0644)
	defer func() { os.Remove(tempIn); os.Remove(tempOut) }()

	exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", tempOut).Run()

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
