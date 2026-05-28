package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 🚀 API RESPONSE STRUCT
// ==========================================
type SilentMusicAPIResponse struct {
	Success     bool   `json:"success"`
	Title       string `json:"title"`
	Resolution  string `json:"resolution"`
	DownloadURL string `json:"download_url"`
}

// ==========================================
// 🎧 THE VIP MUSIC MIXER ENGINE (STUDIO EDITION)
// ==========================================
func handleMusicMixer(client *whatsmeow.Client, v *events.Message, args string) {
	// 1. چیک کریں کہ کسی میسج کا رپلائی کیا ہے
	contextInfo := v.Message.GetExtendedTextMessage().GetContextInfo()
	if contextInfo == nil || contextInfo.GetQuotedMessage() == nil {
		replyMessage(client, v, "❌ *Error:* Please reply to a voice note with `.music` or `.music [sad/happy/etc]`")
		return
	}

	audioMsg := contextInfo.GetQuotedMessage().GetAudioMessage()
	if audioMsg == nil {
		replyMessage(client, v, "❌ *Error:* This command only works for voice notes.")
		return
	}

	// 2. سرچ کوئری (خالص پیانو اور ڈھول/بیٹ، بغیر کسی سنگر کی آواز کے)
	searchQuery := "pure piano and emotional trap beat instrumental no vocals short"
	if args != "" {
		searchQuery = args + " instrumental pure beat no vocals short"
	}


	fmt.Printf("\n===================================================\n")
	fmt.Printf("🎧 [MUSIC MIXER] STUDIO PROCESS STARTED\n")
	fmt.Printf("===================================================\n")
	fmt.Printf("🔍 1. Search Query: '%s'\n", searchQuery)

	react(client, v, "⏳")

	// 3. عارضی فائل نیمز
	timestamp := time.Now().UnixNano()
	voiceFile := fmt.Sprintf("voice_%d.ogg", timestamp)
	musicFile := fmt.Sprintf("music_%d.mp3", timestamp)
	finalFile := fmt.Sprintf("final_%d.ogg", timestamp)

	// فائلیں خود بخود ڈیلیٹ کرنے کے لیے
	defer func() {
		os.Remove(voiceFile)
		os.Remove(musicFile)
		os.Remove(finalFile)
	}()

	// ==========================================
	// 🎙️ STEP A: یوزر کی وائس ڈاؤن لوڈ کریں
	// ==========================================
	audioData, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Printf("❌ [MUSIC] Voice Download Error: %v\n", err)
		replyMessage(client, v, "❌ *Error:* Failed to download your voice note.")
		return
	}
	os.WriteFile(voiceFile, audioData, 0644)
	fmt.Printf("✅ 2. User Voice Downloaded successfully.\n")

	// ==========================================
	// 🎵 STEP B: یوٹیوب سے میوزک سرچ (صرف شارٹ ویڈیوز)
	// ==========================================
	// فلٹر لگا ہوا ہے کہ 5 منٹ سے چھوٹی ویڈیو لے کر آئے تاکہ API ٹائم آؤٹ نہ ہو۔
	cmd := exec.Command("yt-dlp", "ytsearch5:"+searchQuery, "--match-filter", "duration < 300", "--flat-playlist", "--print", "id")
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) == 0 {
		fmt.Printf("❌ [MUSIC] YT-DLP Error: %v\nOutput: %s\n", err, string(out))
		replyMessage(client, v, "❌ *Error:* Failed to find a short background music track.")
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		replyMessage(client, v, "❌ *Error:* No short music found under 5 minutes.")
		return
	}

	vidID := strings.TrimSpace(lines[0])
	ytUrl := "https://www.youtube.com/watch?v=" + vidID
	fmt.Printf("🔗 3. Found YouTube URL (Under 5 mins): %s\n", ytUrl)

	// ==========================================
	// 🌐 STEP C: آپ کی API سے ڈائریکٹ ڈاؤنلوڈ لنک نکالنا
	// ==========================================
	apiUrl := fmt.Sprintf("https://youtube-dwn-production-a806.up.railway.app/api/download?url=%s&resolution=mp3", url.QueryEscape(ytUrl))
	fmt.Printf("🌐 4. Hitting API: %s\n", apiUrl)
	
	apiResp, err := http.Get(apiUrl)
	if err != nil {
		fmt.Printf("❌ [MUSIC] API Network Error: %v\n", err)
		replyMessage(client, v, "❌ *Error:* Failed to connect to Silent API.")
		return
	}
	defer apiResp.Body.Close()

	bodyBytes, err := io.ReadAll(apiResp.Body)
	if err != nil {
		fmt.Printf("❌ [MUSIC] Failed to read API body: %v\n", err)
		replyMessage(client, v, "❌ *Error:* Failed to read API response.")
		return
	}

	fmt.Printf("📦 5. RAW API RESPONSE:\n%s\n", string(bodyBytes))

	var apiData SilentMusicAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiData); err != nil {
		fmt.Printf("❌ [MUSIC] JSON Parse Error: %v\n", err)
		replyMessage(client, v, "❌ *Error:* API Response is not valid JSON.")
		return
	}

	if !apiData.Success || apiData.DownloadURL == "" {
		fmt.Printf("❌ [MUSIC] API Success is False OR DownloadURL is empty!\n")
		replyMessage(client, v, "❌ *Error:* Failed to extract direct download link.")
		return
	}
	
	fmt.Printf("✅ 6. Extracted Audio Download URL: %s\n", apiData.DownloadURL)

	// ==========================================
	// 📥 STEP D: اصل MP3 ڈاؤن لوڈ کریں
	// ==========================================
	musicResp, err := http.Get(apiData.DownloadURL)
	if err != nil {
		fmt.Printf("❌ [MUSIC] MP3 Download Network Error: %v\n", err)
		replyMessage(client, v, "❌ *Error:* Failed to fetch music file.")
		return
	}
	defer musicResp.Body.Close()

	mFile, err := os.Create(musicFile)
	if err != nil {
		fmt.Printf("❌ [MUSIC] Failed to create local mp3 file: %v\n", err)
		replyMessage(client, v, "❌ *System Error:* Could not create music file.")
		return
	}
	io.Copy(mFile, musicResp.Body)
	mFile.Close()
	fmt.Printf("✅ 7. MP3 Downloaded and Saved successfully.\n")

	react(client, v, "🎛️") 

	// ==========================================
	// 🎚️ STEP E: FFmpeg VIP مکسنگ (HIGH BASS, ECHO & VIBRATO)
	// ==========================================
	fmt.Printf("🎚️ 8. Starting FFmpeg Mixing (High Bass & Echo)...\n")
	// 🔥 VIP Chorus Filter:
	// bass=g=12 -> آواز مزید بھاری
	// chorus=0.6:0.9:40|60|80... -> یہ 3 مزید مختلف آوازیں پیدا کرے گا (ٹوٹل 4 بندوں کی فیل)
	// aecho -> گونج برقرار رکھی ہے
	// volume=0.25 (Music) -> میوزک کو تھوڑا اوپر کیا ہے تاکہ پیانو اور بیٹس سنائی دیں
	
	filter := "[0:a]bass=g=12:f=110, treble=g=5:f=3000, chorus=0.6:0.9:40|60|80:0.4|0.4|0.3:0.25|0.4|0.3:2|2.5|3, aecho=0.8:0.4:250:0.3, volume=3.5[v]; [1:a]volume=0.25, lowpass=f=4000[bg]; [v][bg]amix=inputs=2:duration=first"



	mixCmd := exec.Command("ffmpeg", "-y",
		"-i", voiceFile,
		"-i", musicFile,
		"-filter_complex", filter,
		"-c:a", "libopus",
		"-b:a", "64k",
		"-vbr", "on",
		finalFile)

	mixOut, err := mixCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("❌ [MUSIC] FFmpeg Error: %v\nOutput: %s\n", err, string(mixOut))
		replyMessage(client, v, "❌ *Processing Error:* Failed to mix audio.")
		return
	}
	fmt.Printf("✅ 9. FFmpeg Mixing Complete!\n")

	// ==========================================
	// 📤 STEP F: فائنل آڈیو کو واٹس ایپ پر بھیجیں (As PTT)
	// ==========================================
	finalData, err := os.ReadFile(finalFile)
	if err != nil {
		fmt.Printf("❌ [MUSIC] Failed to read final.ogg: %v\n", err)
		replyMessage(client, v, "❌ *Error:* Could not read final file.")
		return
	}

	uploaded, err := client.Upload(context.Background(), finalData, whatsmeow.MediaAudio)
	if err != nil {
		fmt.Printf("❌ [MUSIC] WhatsApp Upload Error: %v\n", err)
		replyMessage(client, v, "❌ *Upload Error:* Failed to upload to WhatsApp.")
		return
	}
	fmt.Printf("✅ 10. Uploaded to WhatsApp successfully. Sending message...\n")

	ptt := true
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(finalData))),
			Seconds:       audioMsg.Seconds,
			PTT:           &ptt,
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})

	react(client, v, "✅")
	fmt.Printf("🎉 [MUSIC MIXER] PROCESS FINISHED SUCCESSFULLY!\n")
	fmt.Printf("===================================================\n\n")
}
