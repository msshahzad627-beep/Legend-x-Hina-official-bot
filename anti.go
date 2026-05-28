package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ==========================================
// 🛠️ DATABASE INIT (With Stealth Trigger)
// ==========================================
// ==========================================
// 🛠️ DATABASE INIT (Updated for Anti-Edit)
// ==========================================
func initPersonalLogDB() {
	query := `CREATE TABLE IF NOT EXISTS personal_log_settings (
		bot_jid TEXT PRIMARY KEY,
		anti_delete_group TEXT DEFAULT '',
		anti_vv_group TEXT DEFAULT '',
		anti_vv_trigger TEXT DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS message_cache (
		msg_id TEXT PRIMARY KEY,
		sender_jid TEXT,
		msg_content BLOB,
		timestamp INTEGER
	);`
	settingsDB.Exec(query)

	// پرانے ٹیبل میں نئے کالمز ایڈ کر رہے ہیں (اگر پہلے سے ہیں تو ایرر اگنور ہوگا)
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_vv_trigger TEXT DEFAULT ''")
	settingsDB.Exec("ALTER TABLE personal_log_settings ADD COLUMN anti_edit_group TEXT DEFAULT ''")
}

// =========================================
// ==========================================
func handleAntiDeleteSave(client *whatsmeow.Client, v *events.Message) {
	// اگر میسج خالی ہے یا بوٹ نے خود بھیجا ہے، تو اگنور کریں
	if v.Message == nil || v.Info.IsFromMe { return }

	botJID := client.Store.ID.ToNonAD().User

	// 🔍 ڈیٹا بیس سے چیک کریں کہ کیا اینٹی ڈیلیٹ یا اینٹی ایڈیٹ آن ہے؟
	var adGroup, aeGroup string
	err := settingsDB.QueryRow("SELECT anti_delete_group, anti_edit_group FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&adGroup, &aeGroup)
	
	// اگر کوئی ریکارڈ نہیں ملا، یا دونوں لاگ گروپس خالی ہیں (یعنی فیچرز آف ہیں)، تو میسج سیو مت کرو!
	if err != nil || (adGroup == "" && aeGroup == "") {
		return
	}

	// اگر دونوں میں سے کوئی ایک بھی آن ہے، تو پھر میسج کو کیش (Cache) کر لو
	msgBytes, protoErr := proto.Marshal(v.Message)
	if protoErr == nil {
		settingsDB.Exec("INSERT OR REPLACE INTO message_cache (msg_id, sender_jid, msg_content, timestamp) VALUES (?, ?, ?, ?)", 
			v.Info.ID, v.Info.Sender.String(), msgBytes, v.Info.Timestamp.Unix())
	}
}



// ==========================================
// 🛡️ ANTI-DELETE TOGGLE
// ==========================================
func handleAntiDeleteToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, "❌ Use: `.antidelete on` or `.antidelete off`")
		return
	}
	
	botJID := client.Store.ID.ToNonAD().User
	chatJID := v.Info.Chat.ToNonAD().String()
	
	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	var currentGroup string
	err := settingsDB.QueryRow("SELECT anti_delete_group FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&currentGroup)
	if err != nil { currentGroup = "" }

	if args == "on" {
		if currentGroup == chatJID {
			replyMessage(client, v, "⚠️ *Already ON:* This is already your personal Log Group for Anti-Delete.")
			return
		}
		
		settingsDB.Exec("UPDATE personal_log_settings SET anti_delete_group = ? WHERE bot_jid = ?", chatJID, botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Personal Log Group Activated!* Private deleted messages will now be forwarded here.")
		
	} else if args == "off" {
		if currentGroup != chatJID {
			replyMessage(client, v, "⚠️ *Error:* You can only turn this OFF from the exact Log Group where you turned it ON.")
			return
		}
		
		settingsDB.Exec("UPDATE personal_log_settings SET anti_delete_group = '' WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Personal Log Group Deactivated!* Anti-Delete forwarding is now OFF.")
	}
}

// ==========================================
// 🛡️ ANTI-VV TOGGLE & TRIGGER SETTER (Updated)
// ==========================================
func handleAntiVVToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	
	args = strings.TrimSpace(args)
	parts := strings.Fields(args)
	
	if len(parts) == 0 {
		replyMessage(client, v, "❌ Use: `.antivv on`, `.antivv off`, or `.antivv set <word>`")
		return
	}
	
	botJID := client.Store.ID.ToNonAD().User
	chatJID := v.Info.Chat.ToNonAD().String()
	cmd := strings.ToLower(parts[0])
	
	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	var currentGroup, currentTrigger string
	settingsDB.QueryRow("SELECT anti_vv_group, anti_vv_trigger FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&currentGroup, &currentTrigger)

	if cmd == "on" {
		if !v.Info.IsGroup {
			replyMessage(client, v, "❌ *Error:* Please use this command inside your intended 'Log Group'.")
			return
		}
		if currentGroup == chatJID {
			replyMessage(client, v, "⚠️ *Already ON:* This is already your personal Stealth Log Group.")
			return
		}
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_group = ? WHERE bot_jid = ?", chatJID, botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Stealth Log Group Activated!* Media extracted via trigger word will be forwarded here.")
		
	} else if cmd == "off" {
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_group = '' WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Stealth Log Group Deactivated!*")
		
	} else if cmd == "set" {
		if len(parts) < 2 {
			replyMessage(client, v, "❌ *Error:* Please provide a trigger word. Example: `.antivv set nice`")
			return
		}
		triggerWord := strings.ToLower(parts[1])
		settingsDB.Exec("UPDATE personal_log_settings SET anti_vv_trigger = ? WHERE bot_jid = ?", triggerWord, botJID)
		react(client, v, "✅")
		replyMessage(client, v, fmt.Sprintf("🕵️ *Stealth Trigger Set!*\nNow, replying to any media with exactly *\"%s\"* will secretly forward it to your Log Group.", triggerWord))
		
	} else {
		replyMessage(client, v, "❌ Invalid command. Use `on`, `off`, or `set <word>`")
	}
}

// ==========================================
// 🚫 ANTI-DELETE REVOKE HANDLER
// ==========================================
// ==========================================
// 🚫 ANTI-DELETE REVOKE HANDLER (Central Log Group)
// ==========================================
func handleAntiDeleteRevoke(client *whatsmeow.Client, v *events.Message) {
	if v.Info.IsFromMe { return }

	botJID := client.Store.ID.ToNonAD().User
	botFullJID := client.Store.ID.ToNonAD().String()
	deletedMsgID := v.Message.GetProtocolMessage().GetKey().GetID()
	senderJID := v.Info.Sender.ToNonAD().User

	// 🔍 کیش سے اوریجنل میسج نکالیں
	var rawMsg []byte
	var msgTimestamp int64
	err := settingsDB.QueryRow("SELECT msg_content, timestamp FROM message_cache WHERE msg_id = ?", deletedMsgID).Scan(&rawMsg, &msgTimestamp)
	if err != nil { return } 

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	// 📥 پرسنل لاگ گروپ نکالیں جہاں کمانڈ آن کی تھی
	var logGroup string
	err = settingsDB.QueryRow("SELECT anti_delete_group FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&logGroup)
	if err != nil || logGroup == "" { return }

	targetJID, _ := types.ParseJID(logGroup)

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	deletedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := strings.Split(senderJID, "@")[0]

	// 📝 چیک کریں کہ میسج پرائیویٹ چیٹ کا ہے یا گروپ کا
	chatContext := "👤 *Type:* Private Chat (DM)"
	if v.Info.IsGroup {
		chatContext = fmt.Sprintf("👥 *Group JID:* %s", v.Info.Chat.ToNonAD().String())
	}

	warningText := fmt.Sprintf(`❖ ── ✦ 🚫 𝗔𝗡𝗧𝗜-𝗗𝗘𝗟𝗘𝗧𝗘 𝗔𝗟𝗘𝗥𝗧 🚫 ✦ ── ❖

👤 *Sender:* @%s
%s
📅 *Sent At:* %s
🗑️ *Deleted At:* %s

_Attempted to delete this message!_
╰──────────────────────╯`, cleanSender, chatContext, sentTime, deletedTime)

	// 1️⃣ اوریجنل میسج آپ کے لاگ گروپ میں فارورڈ ہوگا
	resp, sendErr := client.SendMessage(context.Background(), targetJID, &originalMsg)
	
	// 2️⃣ پھر اس کے نیچے الرٹ کارڈ بھیجیں گے
	if sendErr == nil {
		replyMsg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(warningText),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(resp.ID), 
					Participant:   proto.String(botFullJID), 
					QuotedMessage: &originalMsg,
					MentionedJID:  []string{},
				},
			},
		}
		client.SendMessage(context.Background(), targetJID, replyMsg)
	}
}


// ==========================================
// 🕵️ STEALTH MEDIA EXTRACTOR (The Trigger Hack)
// ==========================================
func handleStealthVVTrigger(client *whatsmeow.Client, v *events.Message) {
	// 🛡️ 1. Crash Preventer (Panic Recovery)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [STEALTH CRASH PREVENTED]: %v\n", r)
		}
	}()

	// 🛡️ 2. سب سے اہم: Database Connection Check! 
	// اگر ڈیٹا بیس ابھی لوڈ نہیں ہوا، تو چپ چاپ واپس مڑ جاؤ، کریش مت کرو!
	if settingsDB == nil {
		return
	}

	// 🛡️ 3. Client & Store Validation
	if client == nil || client.Store == nil || client.Store.ID == nil {
		return
	}

	botJID := client.Store.ID.ToNonAD().User

	var logGroup, triggerWord string
	err := settingsDB.QueryRow("SELECT anti_vv_group, anti_vv_trigger FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&logGroup, &triggerWord)
	if err != nil || logGroup == "" || triggerWord == "" {
		return
	}

	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil { return } 

	msgText := strings.ToLower(strings.TrimSpace(extMsg.GetText()))
	if msgText != triggerWord {
		return 
	}

	if extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		return
	}

	quoted := extMsg.ContextInfo.QuotedMessage
	var data []byte
	var extractErr error
	var finalMsg waProto.Message
	var mType whatsmeow.MediaType

	extractMedia := func(m *waProto.Message) bool {
		if img := m.GetImageMessage(); img != nil {
			data, extractErr = client.Download(context.Background(), img)
			mType = whatsmeow.MediaImage
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.ImageMessage = &waProto.ImageMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("image/jpeg"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if vid := m.GetVideoMessage(); vid != nil {
			data, extractErr = client.Download(context.Background(), vid)
			mType = whatsmeow.MediaVideo
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.VideoMessage = &waProto.VideoMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("video/mp4"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))),
				}
				return true
			}
		} else if aud := m.GetAudioMessage(); aud != nil {
			data, extractErr = client.Download(context.Background(), aud)
			mType = whatsmeow.MediaAudio
			if extractErr == nil {
				up, _ := client.Upload(context.Background(), data, mType)
				finalMsg.AudioMessage = &waProto.AudioMessage{
					URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
					MediaKey: up.MediaKey, Mimetype: proto.String("audio/ogg; codecs=opus"),
					FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
					FileLength: proto.Uint64(uint64(len(data))), PTT: proto.Bool(true),
				}
				return true
			}
		}
		return false
	}

	if vo := quoted.GetViewOnceMessage(); vo != nil {
		extractMedia(vo.GetMessage())
	} else if vo2 := quoted.GetViewOnceMessageV2(); vo2 != nil {
		extractMedia(vo2.GetMessage())
	} else if vo3 := quoted.GetViewOnceMessageV2Extension(); vo3 != nil {
		extractMedia(vo3.GetMessage())
	} else {
		extractMedia(quoted)
	}

	if data != nil && len(data) > 0 {
		targetJID, _ := types.ParseJID(logGroup)
		botFullJID := client.Store.ID.ToNonAD().String()
		cleanSender := strings.Split(v.Info.Chat.User, "@")[0]
		
		// 1️⃣ پہلے میڈیا سینڈ کریں
		resp, sendErr := client.SendMessage(context.Background(), targetJID, &finalMsg)
		
		// 2️⃣ پھر اس کو ریپلائی کر کے کارڈ بھیجیں
		if sendErr == nil {
			caption := fmt.Sprintf(`❖ ── ✦ 🕵️ 𝗦𝗧𝗘𝗔𝗟𝗧𝗛 𝗘𝗫𝗧𝗥𝗔𝗖𝗧 ✦ ── ❖

👤 *From Chat:* @%s
🔑 *Trigger:* "%s"
╰──────────────────────╯`, cleanSender, triggerWord)

			replyMsg := &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(caption),
					ContextInfo: &waProto.ContextInfo{
						StanzaID:      proto.String(resp.ID),
						Participant:   proto.String(botFullJID),
						QuotedMessage: &finalMsg,
						MentionedJID:  []string{},
					},
				},
			}
			client.SendMessage(context.Background(), targetJID, replyMsg)
		}
	}
}


// ==========================================
// ✏️ ANTI-EDIT TOGGLE
// ==========================================
func handleAntiEditToggle(client *whatsmeow.Client, v *events.Message, args string) {
	initPersonalLogDB()
	if !v.Info.IsGroup {
		replyMessage(client, v, "❌ *Error:* Please use this command inside your intended 'Log Group'.")
		return
	}
	args = strings.ToLower(strings.TrimSpace(args))
	if args != "on" && args != "off" {
		replyMessage(client, v, "❌ Use: `.antiedit on` or `.antiedit off`")
		return
	}
	
	botJID := client.Store.ID.ToNonAD().User
	chatJID := v.Info.Chat.ToNonAD().String()
	
	settingsDB.Exec("INSERT OR IGNORE INTO personal_log_settings (bot_jid) VALUES (?)", botJID)

	var currentGroup string
	err := settingsDB.QueryRow("SELECT anti_edit_group FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&currentGroup)
	if err != nil { currentGroup = "" }

	if args == "on" {
		if currentGroup == chatJID {
			replyMessage(client, v, "⚠️ *Already ON:* This is already your personal Log Group for Anti-Edit.")
			return
		}
		
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_group = ? WHERE bot_jid = ?", chatJID, botJID)
		react(client, v, "✅")
		replyMessage(client, v, "✅ *Anti-Edit Log Group Activated!*\nEdited messages from both Private & Groups will now be forwarded here.")
		
	} else if args == "off" {
		if currentGroup != chatJID {
			replyMessage(client, v, "⚠️ *Error:* You can only turn this OFF from the exact Log Group where you turned it ON.")
			return
		}
		
		settingsDB.Exec("UPDATE personal_log_settings SET anti_edit_group = '' WHERE bot_jid = ?", botJID)
		react(client, v, "✅")
		replyMessage(client, v, "❌ *Anti-Edit Log Group Deactivated!*")
	}
}

// ==========================================
// ✏️ ANTI-EDIT HANDLER (Catches Edited Msgs)
// ==========================================
// ==========================================
// ✏️ ANTI-EDIT HANDLER (Central Log Group)
// ==========================================
func handleAntiEdit(client *whatsmeow.Client, v *events.Message) {
	if v.Info.IsFromMe { return }

	protocolMsg := v.Message.GetProtocolMessage()
	if protocolMsg == nil || protocolMsg.GetType() != waProto.ProtocolMessage_MESSAGE_EDIT { return }

	botJID := client.Store.ID.ToNonAD().User
	botFullJID := client.Store.ID.ToNonAD().String()
	
	// 📥 پرسنل لاگ گروپ نکالیں جہاں اینٹی ایڈیٹ آن ہے
	var logGroup string
	err := settingsDB.QueryRow("SELECT anti_edit_group FROM personal_log_settings WHERE bot_jid = ?", botJID).Scan(&logGroup)
	if err != nil || logGroup == "" { return }

	targetJID, _ := types.ParseJID(logGroup)
	originalMsgID := protocolMsg.GetKey().GetID()
	senderJID := v.Info.Sender.ToNonAD().User

	// 📝 نیا تبدیل شدہ ٹیکسٹ نکالیں
	editedMsg := protocolMsg.GetEditedMessage()
	newText := ""
	if editedMsg != nil {
		if editedMsg.GetConversation() != "" {
			newText = editedMsg.GetConversation()
		} else if editedMsg.GetExtendedTextMessage() != nil {
			newText = editedMsg.GetExtendedTextMessage().GetText()
		}
	}

	// 🔍 ڈیٹا بیس کیش سے پرانا اصلی میسج ڈھونڈیں
	var rawMsg []byte
	var msgTimestamp int64
	err = settingsDB.QueryRow("SELECT msg_content, timestamp FROM message_cache WHERE msg_id = ?", originalMsgID).Scan(&rawMsg, &msgTimestamp)
	if err != nil { return } 

	var originalMsg waProto.Message
	proto.Unmarshal(rawMsg, &originalMsg)

	loc, _ := time.LoadLocation("Asia/Karachi")
	sentTime := time.Unix(msgTimestamp, 0).In(loc).Format("02 Jan 2006, 03:04 PM")
	editedTime := time.Now().In(loc).Format("02 Jan 2006, 03:04 PM")
	cleanSender := strings.Split(senderJID, "@")[0]

	// 📝 چیک کریں کہ میسج پرائیویٹ چیٹ کا ہے یا گروپ کا
	chatContext := "👤 *Type:* Private Chat (DM)"
	if v.Info.IsGroup {
		chatContext = fmt.Sprintf("👥 *Group JID:* %s", v.Info.Chat.ToNonAD().String())
	}

	warningText := fmt.Sprintf(`❖ ── ✦ ✏️ 𝗔𝗡𝗧block𝗜-𝗘𝗗block𝗜𝗧 𝗔𝗟block𝗘𝗥𝗧 ✏️ ✦ ── ❖

👤 *Sender:* @%s
%s
📅 *Sent At:* %s
✏️ *Edited At:* %s

📝 *New Edited Text:*
_%s_
╰──────────────────────╯`, cleanSender, chatContext, sentTime, editedTime, newText)

	// 1️⃣ پرانا اصلی میسج آپ کے لاگ گروپ میں جائے گا
	resp, sendErr := client.SendMessage(context.Background(), targetJID, &originalMsg)
	
	// 2️⃣ اور الرٹ کارڈ میں گروپ اور بندہ دونوں شو ہوں گے
	if sendErr == nil {
		replyMsg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(warningText),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(resp.ID), 
					Participant:   proto.String(botFullJID), 
					QuotedMessage: &originalMsg,
					MentionedJID:  []string{},
				},
			},
		}
		client.SendMessage(context.Background(), targetJID, replyMsg)
	}
}
