package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 🛠️ HELPER: MEDIA DOWNLOADER
// ==========================================
func getQuotedMedia(client *whatsmeow.Client, v *events.Message) ([]byte, string, string) {
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		if img := v.Message.GetImageMessage(); img != nil {
			data, _ := client.Download(context.Background(), img)
			return data, "image", ".jpg"
		} else if vid := v.Message.GetVideoMessage(); vid != nil {
			data, _ := client.Download(context.Background(), vid)
			return data, "video", ".mp4"
		}
		return nil, "", ""
	}

	quoted := extMsg.ContextInfo.QuotedMessage
	if img := quoted.GetImageMessage(); img != nil {
		data, _ := client.Download(context.Background(), img)
		return data, "image", ".jpg"
	} else if vid := quoted.GetVideoMessage(); vid != nil {
		data, _ := client.Download(context.Background(), vid)
		return data, "video", ".mp4"
	} else if stk := quoted.GetStickerMessage(); stk != nil {
		data, _ := client.Download(context.Background(), stk)
		if stk.GetIsAnimated() { return data, "video", ".webp" }
		return data, "image", ".webp"
	}

	return nil, "", ""
}

// ===// ==========================================
// 🎨 COMMAND: .sticker / .s (NO CUT + 100% TRANSPARENT BORDERS)
// ==========================================
func handleSticker(client *whatsmeow.Client, v *events.Message) {
	data, mediaType, ext := getQuotedMedia(client, v)
	if len(data) == 0 {
		replyMessage(client, v, "❌ Please reply to an Image or Video to make a sticker.")
		return
	}

	react(client, v, "⏳")

	tempIn := fmt.Sprintf("./data/temp_in_%d%s", time.Now().UnixNano(), ext)
	tempOut := fmt.Sprintf("./data/temp_out_%d.webp", time.Now().UnixNano())
	os.WriteFile(tempIn, data, 0644)
	defer os.Remove(tempIn)
	defer os.Remove(tempOut)

	var cmd *exec.Cmd
	isAnimated := false

	// 🌟 MAGIC LOGIC: 
	// force_original_aspect_ratio=decrease (لمبائی/چوڑائی خراب نہیں کرے گا)
	// format=rgba (ٹرانسپیرنسی کو ان ایبل کرے گا)
	// color=black@0 (سائیڈوں کی جگہ کو 100% غائب کر دے گا)
	if mediaType == "image" {
		cmd = exec.Command("ffmpeg", "-y", "-i", tempIn,
			"-vcodec", "libwebp",
			"-vf", "scale=512:512:force_original_aspect_ratio=decrease,format=rgba,pad=512:512:(ow-iw)/2:(oh-ih)/2:color=black@0",
			tempOut)
	} else {
		isAnimated = true
		cmd = exec.Command("ffmpeg", "-y", "-i", tempIn,
			"-vcodec", "libwebp",
			"-vf", "fps=15,scale=512:512:force_original_aspect_ratio=decrease,format=rgba,pad=512:512:(ow-iw)/2:(oh-ih)/2:color=black@0",
			"-loop", "0", "-preset", "default", "-an", "-vsync", "0",
			"-q:v", "40", "-t", "00:00:10", tempOut)
	}

	if err := cmd.Run(); err != nil {
		replyMessage(client, v, "❌ FFmpeg Engine Failed to create sticker.")
		return
	}

	stkData, err := os.ReadFile(tempOut)
	if err != nil || len(stkData) == 0 {
		replyMessage(client, v, "❌ Sticker generation failed.")
		return
	}

	up, err := client.Upload(context.Background(), stkData, whatsmeow.MediaImage)
	if err != nil { return }

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		StickerMessage: &waProto.StickerMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String("image/webp"),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(stkData))),
			IsAnimated: proto.Bool(isAnimated),
			ContextInfo: &waProto.ContextInfo{
				StanzaID: proto.String(v.Info.ID), Participant: proto.String(v.Info.Sender.String()), QuotedMessage: v.Message,
			},
		},
	})
	react(client, v, "✅")
}


// ==========================================
// 🖼️ COMMAND: .toimg
// ==========================================
// ==========================================
// 🖼️ COMMAND: .toimg
// ==========================================
func handleToImg(client *whatsmeow.Client, v *events.Message) {
	data, _, ext := getQuotedMedia(client, v)
	if len(data) == 0 || ext != ".webp" {
		replyMessage(client, v, "❌ Please reply to a Sticker.")
		return
	}
	react(client, v, "⏳")

	tempIn := fmt.Sprintf("./data/in_%d.webp", time.Now().UnixNano())
	tempOut := fmt.Sprintf("./data/out_%d.png", time.Now().UnixNano()) // 🌟 PNG transparency بچانے کے لیے
	os.WriteFile(tempIn, data, 0644)
	defer os.Remove(tempIn)
	defer os.Remove(tempOut)

	// 🛠️ FIX: output کی جگہ tempOut کر دیا گیا ہے
	exec.Command("ffmpeg", "-y", "-i", tempIn, tempOut).Run()

	imgData, err := os.ReadFile(tempOut)
	if err != nil || len(imgData) == 0 { return }

	up, _ := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String("image/png"),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(imgData))), Caption: proto.String("🎨 ✨ HINA ❤️ x 🔥 LEGEND ✨"),
			ContextInfo: &waProto.ContextInfo{ StanzaID: proto.String(v.Info.ID), Participant: proto.String(v.Info.Sender.String()), QuotedMessage: v.Message },
		},
	})
	react(client, v, "✅")
}


// ==========================================
// 🎬 COMMAND: .tovideo / .togif (ImageMagick + FFmpeg Logic)
// ==========================================
func handleToVideo(client *whatsmeow.Client, v *events.Message, isGif bool) {
	data, _, ext := getQuotedMedia(client, v)
	if len(data) == 0 || ext != ".webp" {
		replyMessage(client, v, "❌ Please reply to an Animated Sticker.")
		return
	}
	react(client, v, "⏳")

	inputWebP := fmt.Sprintf("./data/in_%d.webp", time.Now().UnixNano())
	tempGif := fmt.Sprintf("./data/temp_%d.gif", time.Now().UnixNano())
	outputMp4 := fmt.Sprintf("./data/out_%d.mp4", time.Now().UnixNano())

	os.WriteFile(inputWebP, data, 0644)
	defer os.Remove(inputWebP)
	defer os.Remove(tempGif)
	defer os.Remove(outputMp4)

	// 🌟 STEP 1: ImageMagick کے ذریعے WebP کو GIF میں تبدیل کریں (Animation بچانے کے لیے)
	cmdConvert := exec.Command("convert", inputWebP, "-coalesce", tempGif)
	if err := cmdConvert.Run(); err != nil {
		replyMessage(client, v, "❌ Failed to parse sticker animation. Ensure ImageMagick is installed.")
		return
	}

	// 🌟 STEP 2: اب GIF کو FFmpeg کے ذریعے MP4 بنائیں
	cmd := exec.Command("ffmpeg", "-y",
		"-i", tempGif,
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p",
		"-c:v", "libx264",
		"-preset", "faster",
		"-crf", "26",
		"-movflags", "+faststart",
		"-pix_fmt", "yuv420p",
		"-t", "10",
		outputMp4)
	
	if err := cmd.Run(); err != nil {
		replyMessage(client, v, "❌ Graphics Engine failed to render video.")
		return
	}

	vidData, err := os.ReadFile(outputMp4)
	if err != nil || len(vidData) == 0 { return }

	up, _ := client.Upload(context.Background(), vidData, whatsmeow.MediaVideo)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String("video/mp4"),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(vidData))), GifPlayback: proto.Bool(isGif),
			Caption: proto.String("🎨✨ HINA ❤️ x 🔥 LEGEND ✨"),
			ContextInfo: &waProto.ContextInfo{ StanzaID: proto.String(v.Info.ID), Participant: proto.String(v.Info.Sender.String()), QuotedMessage: v.Message },
		},
	})
	react(client, v, "✅")
}

// ==========================================
// 🔗 COMMAND: .tourl (Catbox Uploader)
// ==========================================
func handleToUrl(client *whatsmeow.Client, v *events.Message) {
	data, _, ext := getQuotedMedia(client, v)
	if len(data) == 0 {
		replyMessage(client, v, "❌ Please reply to any Image, Video, or Sticker to upload.")
		return
	}
	react(client, v, "⏳")

	tempFile := fmt.Sprintf("./data/upload_%d%s", time.Now().UnixNano(), ext)
	os.WriteFile(tempFile, data, 0644)
	defer os.Remove(tempFile)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("reqtype", "fileupload")
	
	part, _ := writer.CreateFormFile("fileToUpload", filepath.Base(tempFile))
	part.Write(data)
	writer.Close()

	req, _ := http.NewRequest("POST", "https://catbox.moe/user/api.php", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		replyMessage(client, v, "❌ Failed to upload media.")
		return
	}
	defer resp.Body.Close()

	linkData, _ := io.ReadAll(resp.Body)
	replyMessage(client, v, fmt.Sprintf("🌐 *Media Uploaded!*\n\n🔗 *URL:* %s", string(linkData)))
	react(client, v, "✅")
}

// ==========================================
// 🎙️ COMMAND: .toptt (Google TTS)
// ==========================================
// ==========================================
// 🎙️ COMMAND: .toptt (Smart Multilingual & Auto-Script Fix)
// ==========================================
func handleToPTT(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ Please provide text.\n*Example:* `.toptt kiya hal ha`\n*With Language:* `.toptt en Hello bro`")
		return
	}
	react(client, v, "🎙️")

	// 1. زبان چیک کریں
	parts := strings.Fields(args)
	targetLang := "ur" // بائی ڈیفالٹ اردو
	textToSpeak := args

	langMap := map[string]string{
		"ur": "ur", "urdu": "ur",
		"hi": "hi", "hindi": "hi",
		"en": "en", "english": "en",
		"ar": "ar", "arabic": "ar",
		"pa": "pa", "punjabi": "pa",
	}

	if len(parts) > 1 {
		if val, ok := langMap[strings.ToLower(parts[0])]; ok {
			targetLang = val
			textToSpeak = strings.Join(parts[1:], " ")
		}
	}

	// 🔥 VIP JUGAAD: اگر انگلش نہیں ہے (مثلاً اردو/ہندی ہے) تو رومن کو اصلی فونٹ میں بدل دو
	if targetLang != "en" {
		transURL := fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=%s&dt=t&q=%s", targetLang, url.QueryEscape(textToSpeak))
		if trResp, err := http.Get(transURL); err == nil {
			defer trResp.Body.Close()
			var result []interface{}
			if json.NewDecoder(trResp.Body).Decode(&result) == nil && len(result) > 0 {
				if innerArray, ok := result[0].([]interface{}); ok {
					translatedText := ""
					for _, item := range innerArray {
						if strArray, ok2 := item.([]interface{}); ok2 && len(strArray) > 0 {
							translatedText += fmt.Sprintf("%v", strArray[0])
						}
					}
					if translatedText != "" {
						textToSpeak = translatedText // یہ "kiya hal ha" کو "کیا حال ہے" کر دے گا!
					}
				}
			}
		}
	}

	// 2. اصلی اوریجنل فونٹ کے ساتھ وائس جنریٹ کریں
	ttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&tl=%s&client=tw-ob&q=%s", targetLang, url.QueryEscape(textToSpeak))
	
	resp, err := http.Get(ttsURL)
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ Failed to generate audio. Text might be too long.")
		return
	}
	defer resp.Body.Close()

	mp3Data, _ := io.ReadAll(resp.Body)
	tempIn := fmt.Sprintf("./data/tts_in_%d.mp3", time.Now().UnixNano())
	tempOut := fmt.Sprintf("./data/tts_out_%d.ogg", time.Now().UnixNano())
	
	os.WriteFile(tempIn, mp3Data, 0644)
	defer os.Remove(tempIn)
	defer os.Remove(tempOut)

	// 3. FFmpeg کنورژن (WhatsApp PTT)
	exec.Command("ffmpeg", "-y", "-i", tempIn, "-c:a", "libopus", "-b:a", "32k", "-vbr", "on", "-compression_level", "10", "-frame_duration", "20", "-application", "voip", tempOut).Run()

	oggData, err := os.ReadFile(tempOut)
	if err != nil || len(oggData) == 0 { return }
	
	up, _ := client.Upload(context.Background(), oggData, whatsmeow.MediaAudio)

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(oggData))), PTT: proto.Bool(true),
			ContextInfo: &waProto.ContextInfo{ StanzaID: proto.String(v.Info.ID), Participant: proto.String(v.Info.Sender.String()), QuotedMessage: v.Message },
		},
	})
	react(client, v, "✅")
}


// ==========================================
// 🔠 COMMAND: .fancy (50+ Multi-Font Generator)
// ==========================================
func handleFancy(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ Please provide text.\nExample: `.fancy Silent Hackers`")
		return
	}
	react(client, v, "✨")

	// 💎 50+ Premium Fonts Mapped Directly
	fontsList := []string{
		"𝗮𝗯𝗰𝗱𝗲𝗳𝗴𝗵𝗶𝗷𝗸𝗹𝗺𝗻𝗼𝗽𝗾𝗿𝘀𝘁𝘂𝘃𝘄𝘅𝘆𝘇𝗔𝗕𝗖𝗗𝗘𝗙𝗚𝗛𝗜𝗝𝗞𝗟𝗠𝗡𝗢𝗣𝗤𝗥𝗦𝗧𝗨𝗩𝗪𝗫𝗬𝗭", "𝘢𝘣𝘤𝘥𝘦𝘧𝘨𝘩𝘪𝘫𝘬𝘭𝘮𝘯𝘰𝘱𝘲𝘳𝘴𝘵𝘶𝘷𝘸𝘹𝘺𝘻𝘈𝘉𝘊𝘋𝘌𝘍𝘎𝘏𝘐𝘑𝘒𝘓𝘔𝘕𝘖𝘗𝘘𝘙𝘚𝘛𝘜𝘝𝘞𝘟𝘠𝘡",
		"𝙖𝙗𝙘𝙙𝙚𝙛𝙜𝙝𝙞𝙟𝙠𝙡𝙢𝙣𝙤𝙥𝙦𝙧𝙨𝙩𝙪𝙫𝙬𝙭𝙮𝙯𝘼𝘽𝘾𝘿𝙀𝙁𝙂𝙃𝙄𝙅𝙆𝙇𝙈𝙉𝙊𝙋𝙌𝙍𝙎𝙏𝙐𝙑𝙒𝙓𝙔𝙕", "𝚊𝚋𝚌𝚍𝚎𝚏𝚐𝚑𝚒𝚓𝚔𝚕𝚖𝚗𝚘𝚙𝚚𝚛𝚜𝚝𝚞𝚟𝚠𝚡𝚢𝚣𝙰𝙱𝙲𝙳𝙴𝙵𝙶𝙷𝙸𝙹𝙺𝙻𝙼𝙽𝙾𝙿𝚀𝚁𝚂𝚃𝚄𝚅𝚆𝚇𝚈𝚉",
		"𝕒𝕓𝕔𝕕𝕖𝕗𝕘𝕙𝕚𝕛𝕜𝕝𝕞𝕟𝕠𝕡𝕢𝕣𝕤𝕥𝕦𝕧𝕨𝕩𝕪𝕫𝔸𝔹ℂ𝔻𝔼𝔽𝔾ℍ𝕀𝕁𝕂𝕃𝕄ℕ𝕆ℙℚℝ𝕊𝕋𝕌𝕍𝕎𝕏𝕐ℤ", "𝖆𝖇𝖈𝖉𝖊𝖋𝖌𝖍𝖎𝖏𝖐𝖑𝖒𝖓𝖔𝖕𝖖𝖗𝖘𝖙𝖚𝖛𝖜𝖝𝖞𝖟𝕬𝕭𝕮𝕯𝕰𝕱𝕲𝕳𝕴𝕵𝕶𝕷𝕸𝕹𝕺𝕻𝕼𝕽𝕾𝕿𝖀𝖁𝖂𝖃𝖄𝖅",
		"𝒶𝒷𝒸𝒹𝑒𝒻𝑔𝒽𝒾𝒿𝓀𝓁𝓂𝓃𝑜𝓅𝓆𝓇𝓈𝓉𝓊𝓋𝓌𝓍𝓎𝓏𝒜𝐵𝒞𝒟𝐸𝐹𝢢𝐻𝐼𝒥𝒦𝐿𝑀𝒩𝒪𝒫𝒬𝑅𝒮𝒯𝒰𝒱𝒲𝒳𝒴𝒵", "ⓐⓑⓒⓓⓔⓕⓖⓗⓘⓙⓚⓛⓜⓝⓞⓟⓠⓡⓢⓣⓤⓥⓦⓧⓨⓩⒶⒷⒸⒹⒺⒻⒼⒽⒾⒿⓀⓁⓂⓃⓄⓅⓆⓇⓈⓉⓊⓋⓌⓍⓎⓏ",
		"𝐚𝐛𝐜𝐝𝐞𝐟𝐠𝐡𝐢𝐣𝐤𝐥𝐦𝐧𝐨𝐩𝐪𝐫𝐬𝐭𝐮𝐯𝐰𝐱𝐲𝐳𝐀𝐁𝐂𝐃𝐄𝐅𝐆𝐇𝐈𝐉𝐊𝐋𝐌𝐍𝐎𝐏𝐐𝐑𝐒𝐓𝐔𝐕𝐖𝐗𝐘𝐙", "ₐbcdₑfgₕᵢⱼₖₗₘₙₒₚqᵣₛₜᵤᵥwₓyzₐBCDₑFGₕᵢⱼₖₗₘₙₒₚQᵣₛₜᵤᵥWₓYZ",
		"ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖᵠʳˢᵗᵘᵛʷˣʸᶻᴬᴮᶜᴰᴱᶠᴳᴴᴵᴶᴷᴸᴹᴺᴼᴾQᴿˢᵀᵁⱽᵂˣʸᶻ", "卂乃匚ⅅ乇千Ꮆ卄丨ﾌҜㄥ爪几ㄖ卩Ɋ尺丂ㄒㄩᐯ山乂ㄚ乙卂乃匚ⅅ乇千Ꮆ卄丨ﾌҜㄥ爪几ㄖ卩Ɋ尺丂ㄒㄩᐯ山乂ㄚ乙",
		"ꪖꪉᥴᦔꫀᠻᧁꫝ꠸꠹ꪗꪶꪑꪀꪮρꪇ᥅ꪊꪻꪊꪜ᭙᥊ꪗꪅꪖꪉᥴᦔꫀᠻᧁꫝ꠸꠹ꪗꪶꪑꪀꪮρꪇ᥅ꪊꪻꪊꪜ᭙᥊ꪗꪅ", "ᴀʙᴄᴅᴇꜰɢʜɪᴊᴋʟᴍɴᴏᴘQʀꜱᴛᴜᴠᴡxʏᴢᴀʙᴄᴅᴇꜰɢʜɪᴊᴋʟᴍɴᴏᴘQʀꜱᴛᴜᴠᴡxʏᴢ",
		"ค๒ς๔єŦﻮђเןкɭ๓ภ๏קợгรՇยשฬץאչค๒ς๔єŦﻮђเןкɭ๓ภ๏קợгรՇยשฬץאչ", "ąცƈɖɛʄɠɧıʝƙƖɱŋơ℘զཞʂɬų۷ῳҳყźĄƁƇƉƐƑƓƖƘMƠƤQRSƬUVWXƳZ",
		"αв¢∂єƒﻭнιנкℓмησρqяѕтυνωχуzΑΒCDEҒGΗIJKLMNOPQRSΤUVWΧΥZ", "ǟɮƈɖɛʄɢɦɨʝӄʟʍռօքզʀֆȶʊʋաӼʏʐǟɮƈɖɛʄɢɦɨʝӄʟʍռօքզʀֆȶʊʋաӼʏʐ",
		"ᏗᏰፈᎴᏋᎦᎶᏂᎥᏠᏦᏝᎷᏁᎧᎮᎤᏒᏕᏖᏬᏉᏇጀᎩፚᏗᏰፈᎴᏋᎦᎶᏂᎥᏠᏦᏝᎷᏁᎧᎮᎤᏒᏕᏖᏬᏉᏇጀᎩፚ", "ąცƈɖɛʄɠɧıʝƙƖɱŋơ℘զཞʂɬų۷ῳҳყʑĄƁƇƉƐƑƓ-Ɩ-Ƙ-M-OƤ--S-U-W-Y-",
		"åß¢Ðê£ghïjklmñðþqr§†µvwx¥zÄßÇÐÈ£GHÌJKLMÑÖþQR§†ÚVWX¥Z", "äbċdëfgḧïjklmnöpqrsẗüvwxyzÄBĊDËFGḦÏJKLMNÖPQRSṮÜVWXYZ",
		"αႦƈԃҽϝɠԋιʝƙʅɱɳσρϙɾʂƚυʋɯxყȥABCDEFGHIJKLMNOPQRSTUVWXYZ", "ค๒ς๔єŦﻮђเןкɭ๓ภ๏קợгรՇยשฬץאչABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"ΛBCDΣFGHIJKLMИOPQЯƧTUVWXYZΛBCDΣFGHIJKLMИOPQЯƧTUVWXYZ", "⒜⒝⒞⒟⒠⒡⒢⒣⒤⒥⒦⒧⒨⒩⒪⒫⒬⒭⒮⒯⒰⒱⒲⒳⒴⒵⒜⒝⒞⒟⒠⒡⒢⒣⒤⒥⒦⒧⒨⒩⒪⒫⒬⒭⒮⒯⒰⒱⒲⒳⒴⒵",
		"🄐🄑🄒🄓🄔🄕🄖🄗🄘🄙🄚🄛🄜🄝🄞🄟🄠🄡🄢🄣🄤🄥🄦🄧🄨🄩🄐🄑🄒🄓🄔🄕🄖🄗🄘🄙🄚🄛🄜🄝🄞🄟🄠🄡🄢🄣🄤🄥🄦🄧🄨🄩", "🄰🄱🄲🄳🄴🄵🄶🄷🄸🄹🄺🄻🄼🄽🄾🄿🅀🅁🅂🅃🅄🅅🅆🅇🅈🅉🄰🄱🄲🄳🄴🄵🄶🄷🄸🄹🄺🄻🄼🄽🄾🄿🅀🅁🅂🅃🅄🅅🅆🅇🅈🅉",
		"🅐🅑🅒🅓🅔🅕🅖🅗🅘🅙🅚🅛🅜🅝🅞🅟🅠🅡🅢🅣🅤🅥🅦🅧🅨🅩🅐🅑🅒🅓🅔🅕🅖🅗🅘🅙🅚🅛🅜🅝🅞🅟🅠🅡🅢🅣🅤🅥🅦🅧🅨🅩", "🅰🅱🅲🅳🅴🅵🅶🅷🅸🅹🅺🅻🅼🅽🅾🅿🆀🆁🆂🆃🆄🆅🆆🆇🆈🆉🅰🅱🅲🅳🅴🅵🅶🅷🅸🅹🅺🅻🅼🅽🅾🅿🆀🆁🆂🆃🆄🆅🆆🆇🆈🆉",
		"⒜⒝⒞⒟⒠⒡⒢⒣⒤⒥⒦⒧⒨⒩⒪⒫⒬⒭⒮⒯⒰⒱⒲⒳⒴⒵ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ᗩᗷᑕᗪEᖴGᕼIᒍKᒪᗰᑎOᑭQᖇᔕTᑌᐯᗯ᙭YᘔᗩᗷᑕᗪEᖴGᕼIᒍKᒪᗰᑎOᑭQᖇᔕTᑌᐯᗯ᙭Yᘔ",
		"ค๒ς๔єŦﻮђเןкɭ๓ภ๏קợгรՇยשฬץאչABCDEFGHIJKLMNOPQRSTUVWXYZ", "a̶b̶c̶d̶e̶f̶g̶h̶i̶j̶k̶l̶m̶n̶o̶p̶q̶r̶s̶t̶u̶v̶w̶x̶y̶z̶A̶B̶C̶D̶E̶F̶G̶H̶I̶J̶K̶L̶M̶N̶O̶P̶Q̶R̶S̶T̶U̶V̶W̶X̶Y̶Z̶",
		"a̴b̴c̴d̴e̴f̴g̴h̴i̴j̴k̴l̴m̴n̴o̴p̴q̴r̴s̴t̴u̴v̴w̴x̴y̴z̴A̴B̴C̴D̴E̴F̴G̴H̴I̴J̴K̴L̴M̴N̴O̴P̴Q̴R̴S̴T̴U̴V̴W̴X̴Y̴Z̴", "a̷b̷c̷d̷e̷f̷g̷h̷i̷j̷k̷l̷m̷n̷o̷p̷q̷r̷s̷t̷u̷v̷w̷x̷y̷z̷A̷B̷C̷D̷E̷F̷G̷H̷I̷J̷K̷L̷M̷N̷O̷P̷Q̷R̷S̷T̷U̷V̷W̷X̷Y̷Z̷",
		"a̲b̲c̲d̲e̲f̲g̲h̲i̲j̲k̲l̲m̲n̲o̲p̲q̲r̲s̲t̲u̲v̲w̲x̲y̲z̲A̲B̲C̲D̲E̲F̲G̲H̲I̲J̲K̲L̲M̲N̲O̲P̲Q̲R̲S̲T̲U̲V̲W̲X̲Y̲Z̲", "a̳b̳c̳d̳e̳f̳g̳h̳i̳j̳k̳l̳m̳n̳o̳p̳q̳r̳s̳t̳u̳v̳w̳x̳y̳z̳A̳B̳C̳D̳E̳F̳G̳H̳I̳J̳K̳L̳M̳N̳O̳P̳Q̳R̳S̳T̳U̳V̳W̳X̳Y̳Z̳",
		"a̾b̾c̾d̾e̾f̾g̾h̾i̾j̾k̾l̾m̾n̾o̾p̾q̾r̾s̾t̾u̾v̾w̾x̾y̾z̾A̾B̾C̾D̾E̾F̾G̾H̾I̾J̾K̾L̾M̾N̾O̾P̾Q̾R̾S̾T̾U̾V̾W̾X̾Y̾Z̾", "a♥b♥c♥d♥e♥f♥g♥h♥i♥j♥k♥l♥m♥n♥o♥p♥q♥r♥s♥t♥u♥v♥w♥x♥y♥z♥A♥B♥C♥D♥E♥F♥G♥H♥I♥J♥K♥L♥M♥N♥O♥P♥Q♥R♥S♥T♥U♥V♥W♥X♥Y♥Z♥",
		"a͎b͎c͎d͎e͎f͎g͎h͎i͎j͎k͎l͎m͎n͎o͎p͎q͎r͎s͎t͎u͎v͎w͎x͎y͎z͎A͎B͎C͎D͎E͎F͎G͎H͎I͎J͎K͎L͎M͎N͎O͎P͎Q͎R͎S͎T͎U͎V͎W͎X͎Y͎Z͎", "a̽b̽c̽d̽e̽f̽g̽h̽i̽j̽k̽l̽m̽n̽o̽p̽q̽r̽s̽t̽u̽v̽w̽x̽y̽z̽A̽B̽C̽D̽E̽F̽G̽H̽I̽J̽K̽L̽M̽N̽O̽P̽Q̽R̽S̽T̽U̽V̽W̽X̽Y̽Z̽",
		"a✨b✨c✨d✨e✨f✨g✨h✨i✨j✨k✨l✨m✨n✨o✨p✨q✨r✨s✨t✨u✨v✨w✨x✨y✨z✨A✨B✨C✨D✨E✨F✨G✨H✨I✨J✨K✨L✨M✨N✨O✨P✨Q✨R✨S✨T✨U✨V✨W✨X✨Y✨Z✨", "a🔥b🔥c🔥d🔥e🔥f🔥g🔥h🔥i🔥j🔥k🔥l🔥m🔥n🔥o🔥p🔥q🔥r🔥s🔥t🔥u🔥v🔥w🔥x🔥y🔥z🔥A🔥B🔥C🔥D🔥E🔥F🔥G🔥H🔥I🔥J🔥K🔥L🔥M🔥N🔥O🔥P🔥Q🔥R🔥S🔥T🔥U🔥V🔥W🔥X🔥Y🔥Z🔥",
		"a🚀b🚀c🚀d🚀e🚀f🚀g🚀h🚀i🚀j🚀k🚀l🚀m🚀n🚀o🚀p🚀q🚀r🚀s🚀t🚀u🚀v🚀w🚀x🚀y🚀z🚀A🚀B🚀C🚀D🚀E🚀F🚀G🚀H🚀I🚀J🚀K🚀L🚀M🚀N🚀O🚀P🚀Q🚀R🚀S🚀T🚀U🚀V🚀W🚀X🚀Y🚀Z🚀", "a👑b👑c👑d👑e👑f👑g👑h👑i👑j👑k👑l👑m👑n👑o👑p👑q👑r👑s👑t👑u👑v👑w👑x👑y👑z👑A👑B👑C👑D👑E👑F👑G👑H👑I👑J👑K👑L👑M👑N👑O👑P👑Q👑R👑S👑T👑U👑V👑W👑X👑Y👑Z👑",
		"a⚡b⚡c⚡d⚡e⚡f⚡g⚡h⚡i⚡j⚡k⚡l⚡m⚡n⚡o⚡p⚡q⚡r⚡s⚡t⚡u⚡v⚡w⚡x⚡y⚡z⚡A⚡B⚡C⚡D⚡E⚡F⚡G⚡H⚡I⚡J⚡K⚡L⚡M⚡N⚡O⚡P⚡Q⚡R⚡S⚡T⚡U⚡V⚡W⚡X⚡Y⚡Z⚡", "a💀b💀c💀d💀e💀f💀g💀h💀i💀j💀k💀l💀m💀n💀o💀p💀q💀r💀s💀t💀u💀v💀w💀x💀y💀z💀A💀B💀C💀D💀E💀F💀G💀H💀I💀J💀K💀L💀M💀N💀O💀P💀Q💀R💀S💀T💀U💀V💀W💀X💀Y💀Z💀",
		"a🖤b🖤c🖤d🖤e🖤f🖤g🖤h🖤i🖤j🖤k🖤l🖤m🖤n🖤o🖤p🖤q🖤r🖤s🖤t🖤u🖤v🖤w🖤x🖤y🖤z🖤A🖤B🖤C🖤D🖤E🖤F🖤G🖤H🖤I🖤J🖤K🖤L🖤M🖤N🖤O🖤P🖤Q🖤R🖤S🖤T🖤U🖤V🖤W🖤X🖤Y🖤Z🖤", "a❄️b❄️c❄️d❄️e❄️f❄️g❄️h❄️i❄️j❄️k❄️l❄️m❄️n❄️o❄️p❄️q❄️r❄️s❄️t❄️u❄️v❄️w❄️x❄️y❄️z❄️A❄️B❄️C❄️D❄️E❄️F❄️G❄️H❄️I❄️J❄️K❄️L❄️M❄️N❄️O❄️P❄️Q❄️R❄️S❄️T❄️U❄️V❄️W❄️X❄️Y❄️Z❄️",
		"a🌟b🌟c🌟d🌟e🌟f🌟g🌟h🌟i🌟j🌟k🌟l🌟m🌟n🌟o🌟p🌟q🌟r🌟s🌟t🌟u🌟v🌟w🌟x🌟y🌟z🌟A🌟B🌟C🌟D🌟E🌟F🌟G🌟H🌟I🌟J🌟K🌟L🌟M🌟N🌟O🌟P🌟Q🌟R🌟S🌟T🌟U🌟V🌟W🌟X🌟Y🌟Z🌟", "a🌸b🌸c🌸d🌸e🌸f🌸g🌸h🌸i🌸j🌸k🌸l🌸m🌸n🌸o🌸p🌸q🌸r🌸s🌸t🌸u🌸v🌸w🌸x🌸y🌸z🌸A🌸B🌸C🌸D🌸E🌸F🌸G🌸H🌸I🌸J🌸K🌸L🌸M🌸N🌸O🌸P🌸Q🌸R🌸S🌸T🌸U🌸V🌸W🌸X🌸Y🌸Z🌸",
	}

	result := "❖ ── ✦ 𝗙𝗔𝗡𝗖𝗬 𝗧𝗘𝗫𝗧 (50+) ✦ ── ❖\n\n"
	for i, charset := range fontsList {
		result += fmt.Sprintf(" *%d.* %s\n\n", i+1, mapChars(args, charset))
	}
	result += "↬ _Silent Nexus Engine_"

	replyMessage(client, v, result)
}

func mapChars(input string, charset string) string {
	normal := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	normalRunes := []rune(normal)
	charsetRunes := []rune(charset)
	
	output := ""
	for _, char := range input {
		found := false
		for i, nChar := range normalRunes {
			if char == nChar {
				// Prevent index out of range if charset is shorter
				if i < len(charsetRunes) {
					output += string(charsetRunes[i])
					found = true
				}
				break
			}
		}
		if !found { output += string(char) }
	}
	return output
}
