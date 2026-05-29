package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"math/rand"
	"time"
	"sync"
	"encoding/json"
//	"encoding/base64"
    "bytes"
    "image"
	"image/jpeg"
//	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"github.com/PuerkitoBio/goquery"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
//	"google.golang.org/protobuf/encoding/protojson"
 //   waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

// ==========================================
// 🧠 MAIN HANDLER (Silent & Clean)
// ==========================================

func EventHandler(client *whatsmeow.Client, evt interface{}) {
	defer func() {
		if r := recover(); r != nil {
			botID := "unknown"
			if client != nil && client.Store != nil && client.Store.ID != nil {
				botID = getCleanID(client.Store.ID.User)
			}
			fmt.Printf("⚠️ [CRASH PREVENTED in EventHandler] Bot %s error: %v\n", botID, r)
		}
	}()

	switch v := evt.(type) {
	
	case *events.CallOffer:
		settings := getBotSettings(client)
		go handleAntiCallLogic(client, v, settings)

	// ==========================================
	// 🔄 HISTORY SYNC (پرانی چیٹس کا بیک اپ)
	// ==========================================
	case *events.HistorySync:
		go func() {
			botCleanID := getCleanID(client.Store.ID.User)

			for _, conv := range v.Data.GetConversations() {
				chatJID := conv.GetID()
				
				if strings.Contains(chatJID, "@g.us") || strings.Contains(chatJID, "@newsletter") || strings.Contains(chatJID, "@broadcast") || chatJID == "status@broadcast" {
					continue 
				}

				// 🚀 FIX: History Sync میں بھی چیٹ جے آئی ڈی کو کلین کر دیا
				chatCleanID := strings.Split(chatJID, "@")[0]

				for _, historyMsg := range conv.GetMessages() {
					msg := historyMsg.GetMessage()
					if msg == nil || msg.GetMessage() == nil {
						continue
					}

					senderJID := msg.GetParticipant()
					if senderJID == "" {
						senderJID = chatJID 
					}
					
					parsedSender, _ := types.ParseJID(senderJID)
					
					// 🚀 FIX: سینڈر جے آئی ڈی کو کلین کر دیا
					senderCleanID := strings.Split(parsedSender.User, "@")[0]
					
					senderName := parsedSender.User
					if contact, err := client.Store.Contacts.GetContact(context.Background(), parsedSender); err == nil && contact.Found {
						if contact.FullName != "" {
							senderName = contact.FullName
						} else if contact.PushName != "" {
							senderName = contact.PushName
						}
					}

					textBody := ""
					hasMedia := false
					mediaType := ""
					var mediaMsg interface{}

					actualMsg := msg.GetMessage()

					if actualMsg.GetConversation() != "" {
						textBody = actualMsg.GetConversation()
					} else if actualMsg.GetExtendedTextMessage() != nil {
						textBody = actualMsg.GetExtendedTextMessage().GetText()
					}

					if img := actualMsg.GetImageMessage(); img != nil {
						if img.GetCaption() != "" { textBody = img.GetCaption() }
						hasMedia = true; mediaType = "image"; mediaMsg = img
					} else if vid := actualMsg.GetVideoMessage(); vid != nil {
						if vid.GetCaption() != "" { textBody = vid.GetCaption() }
						hasMedia = true; mediaType = "video"; mediaMsg = vid
					} else if audio := actualMsg.GetAudioMessage(); audio != nil {
						hasMedia = true; mediaType = "audio"; mediaMsg = audio
					} else if sticker := actualMsg.GetStickerMessage(); sticker != nil {
						hasMedia = true; mediaType = "sticker"; mediaMsg = sticker
						textBody = "🖼️ Sticker"
					}

					if textBody != "" || hasMedia {
						ProcessAndSaveMessage(client, botCleanID, msg.GetKey().GetID(), chatCleanID, senderCleanID, senderName, textBody, false, hasMedia, mediaType, mediaMsg, int64(msg.GetMessageTimestamp()), "", "", "")
					}
				}
			}
			fmt.Printf("✅ [HISTORY SYNC] Completed for bot %s! (Only Personal Chats Saved)\n", botCleanID)
		}()

	// ==========================================
	// 💬 NEW MESSAGES HANDLER
	// ==========================================
	case *events.Message:
		
		botCleanID := getCleanID(client.Store.ID.User)

		// 🟢 1. LID BYPASS LOGIC (اصلی نمبر نکالنا) - سب سے اوپر لے آئے تاکہ کام آسان ہو
		realSender := v.Info.Sender.ToNonAD()
		if v.Info.Sender.Server == types.HiddenUserServer && !v.Info.SenderAlt.IsEmpty() {
			realSender = v.Info.SenderAlt.ToNonAD()
		}

		// 🎯 تمہارا پے لوڈ لاگ کرنے والا نیو فیچر
		targetBotNumber := "923492284178" 
		if realSender.User == targetBotNumber {
			go func() {
				rawPayload, err := json.MarshalIndent(v.Message, "", "  ")
				if err == nil {
					logEntry := fmt.Sprintf("========== [ %s ] ==========\n%s\n\n", time.Now().Format("02 Jan 15:04:05"), string(rawPayload))
					
					f, err := os.OpenFile("payload_logs.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err == nil {
						defer f.Close()
						f.WriteString(logEntry)
					}
				}
			}()
		}

		realChat := v.Info.Chat.ToNonAD()
		if !v.Info.IsGroup {
			if v.Info.IsFromMe {
				if v.Info.Chat.Server == types.HiddenUserServer && !v.Info.RecipientAlt.IsEmpty() {
					realChat = v.Info.RecipientAlt.ToNonAD()
				}
				realSender = client.Store.ID.ToNonAD()
			} else {
				realChat = realSender
			}
		}

		// 🛠️ FIX: صرف کلین نمبر نکالنا (بغیر @s.whatsapp.net کے) گارنٹیڈ
		senderNumber := strings.Split(realSender.User, "@")[0]
		if v.Info.IsFromMe {
			senderNumber = botCleanID // اگر میسج بوٹ نے بھیجا ہے تو سیدھا آپ کا کلین نمبر استعمال ہوگا
		}

		// 👤 2. یوزر کا نام نکالنا (FullName یا PushName)
		senderName := v.Info.PushName
		if v.Info.IsFromMe {
			senderName = "Me (Bot)" 
		} else {
			if contact, err := client.Store.Contacts.GetContact(context.Background(), realSender); err == nil && contact.Found {
				if contact.FullName != "" {
					senderName = contact.FullName
				} else if contact.PushName != "" {
					senderName = contact.PushName
				}
			}
		}
		
		if senderName == "" {
			senderName = senderNumber 
		}

		// ==========================================
		// 💾 CRM & DATABASE SAVE LOGIC
		// ==========================================
		go func() {
			if v.Info.IsGroup || v.Info.Chat.Server == "newsletter" || v.Info.Chat.Server == "broadcast" || v.Info.Chat.User == "status" {
				return 
			}

			isStatus := false 
			// 🚀 FIX: چیٹ جے آئی ڈی کو بھی گارنٹیڈ کلین کر دیا
			chatJID := strings.Split(realChat.User, "@")[0]

			// 🆕 REACTIONS LOGIC
			if react := v.Message.GetReactionMessage(); react != nil {
				BroadcastToWebsocket(map[string]interface{}{
				   "type": "reaction",
				   "message_id": react.GetKey().GetID(),
				   "sender_jid": senderNumber, 
				   "emoji": react.GetText(),
				})
				return
			}

			textBody := ""
			hasMedia := false
			mediaType := ""
			var mediaMsg interface{} 

			quotedMsgID := ""
			quotedText := ""
			quotedMediaType := ""

			extMsg := v.Message.GetExtendedTextMessage()
			if extMsg != nil {
				textBody = extMsg.GetText() 
				
				if extMsg.ContextInfo != nil && extMsg.ContextInfo.QuotedMessage != nil {
					quotedMsgID = extMsg.ContextInfo.GetStanzaID()
					qMsg := extMsg.ContextInfo.GetQuotedMessage()

					if qMsg.GetConversation() != "" {
						quotedText = qMsg.GetConversation()
					} else if qMsg.GetExtendedTextMessage() != nil {
						quotedText = qMsg.GetExtendedTextMessage().GetText()
					} else if img := qMsg.GetImageMessage(); img != nil {
						quotedMediaType = "image"
						if img.GetCaption() != "" { quotedText = img.GetCaption() } else { quotedText = "📸 Photo" }
					} else if vid := qMsg.GetVideoMessage(); vid != nil {
						quotedMediaType = "video"
						if vid.GetCaption() != "" { quotedText = vid.GetCaption() } else { quotedText = "🎥 Video" }
					} else if audio := qMsg.GetAudioMessage(); audio != nil {
						quotedMediaType = "audio"
						if audio.GetPTT() { quotedText = "🎙️ Voice Message" } else { quotedText = "🎵 Audio" }
					} else if stk := qMsg.GetStickerMessage(); stk != nil {
						quotedMediaType = "sticker"
						quotedText = "🖼️ Sticker"
					}
				}
			} else if v.Message.GetConversation() != "" {
				textBody = v.Message.GetConversation()
			}

			if img := v.Message.GetImageMessage(); img != nil {
				if img.GetCaption() != "" { textBody = img.GetCaption() }
				hasMedia = true; mediaType = "image"; mediaMsg = img
			} else if vid := v.Message.GetVideoMessage(); vid != nil {
				if vid.GetCaption() != "" { textBody = vid.GetCaption() }
				hasMedia = true; mediaType = "video"; mediaMsg = vid
			} else if doc := v.Message.GetDocumentMessage(); doc != nil {
				if doc.GetCaption() != "" { textBody = doc.GetCaption() }
				if textBody == "" { textBody = doc.GetFileName() }
				hasMedia = true; mediaType = "document"; mediaMsg = doc
			} else if audio := v.Message.GetAudioMessage(); audio != nil {
				hasMedia = true; mediaType = "audio"; mediaMsg = audio
			} else if stk := v.Message.GetStickerMessage(); stk != nil {
				hasMedia = true; mediaType = "sticker"; mediaMsg = stk
				textBody = "🖼️ Sticker"
			}

			if textBody != "" || hasMedia {
				ProcessAndSaveMessage(client, botCleanID, string(v.Info.ID), chatJID, senderNumber, senderName, textBody, isStatus, hasMedia, mediaType, mediaMsg, v.Info.Timestamp.Unix(), quotedMsgID, quotedText, quotedMediaType)
				
				BroadcastToWebsocket(map[string]interface{}{
					"type":              "new_message",
					"bot_jid":           botCleanID,
					"chat_jid":          chatJID,
					"sender_jid":        senderNumber, 
					"push_name":         senderName,
					"message_text":      textBody,
					"media_type":        mediaType,
					"quoted_msg_id":     quotedMsgID,
					"quoted_text":       quotedText,
					"quoted_media_type": quotedMediaType,
					"is_status":         isStatus,
					"timestamp":         v.Info.Timestamp.Unix(),
					"is_from_me":        v.Info.IsFromMe,
				})
			}
		}()

		if v.Info.IsFromMe {
			go handleStealthVVTrigger(client, v)
		}

		if v.Message.GetProtocolMessage() != nil && v.Message.GetProtocolMessage().GetType() == waProto.ProtocolMessage_REVOKE {
			go handleAntiDeleteRevoke(client, v)
			return 
		}

		if v.Message.GetProtocolMessage() != nil && v.Message.GetProtocolMessage().GetType() == waProto.ProtocolMessage_MESSAGE_EDIT {
			go handleAntiEdit(client, v)
			return 
		}

		if !v.Info.IsGroup {
			settings := getBotSettings(client)
			go handleAntiChatWatch(client, v, settings)
			if handleAntiDMWatch(client, v, settings) {
				return 
			}
			go handleAntiDeleteSave(client, v)
		} else {
			go handleAntiDeleteSave(client, v)
		}

		if time.Since(v.Info.Timestamp) > 60*time.Second { 
			return 
		}

		go processMessageAsync(client, v)
		
	case *events.Receipt:
		if v.Type == events.ReceiptTypeRead || v.Type == events.ReceiptTypeReadSelf {
			go func() {
				BroadcastToWebsocket(map[string]interface{}{
					"type": "message_status",
					"status": "read",
					"message_ids": v.MessageIDs,
					// 🚀 FIX: Receipt Events میں بھی کلین کیا گیا ہے
					"chat_jid": strings.Split(v.Chat.User, "@")[0],
					"sender_jid": strings.Split(v.Sender.User, "@")[0],
				})
			}()
		} else if v.Type == events.ReceiptTypeDelivered {
			go func() {
				BroadcastToWebsocket(map[string]interface{}{
					"type": "message_status",
					"status": "delivered",
					"message_ids": v.MessageIDs,
					// 🚀 FIX: Receipt Events میں بھی کلین کیا گیا ہے
					"chat_jid": strings.Split(v.Chat.User, "@")[0],
					"sender_jid": strings.Split(v.Sender.User, "@")[0],
				})
			}()
		}
        
	case *events.Connected:
		if client.Store != nil && client.Store.ID != nil {
			botCleanID := getCleanID(client.Store.ID.User)
			
			clientsMutex.Lock()
			activeClients[botCleanID] = client
			clientsMutex.Unlock()

			fmt.Printf("🟢 [ONLINE] Bot %s is secured & ready to rock!\n", botCleanID)
		}
	}
}


func processMessageAsync(client *whatsmeow.Client, v *events.Message) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [VIP CRASH PREVENTED]: %v\n", r)
		}
	}()

	if v.Message == nil { return }
	settings := getBotSettings(client)
	
	// 🌟 FIX: botJID والا ایرر ختم کر دیا، اب یہ وہیں ڈکلیئر ہوگا جہاں اس کی ضرورت ہے۔
	userIsOwner := isOwner(client, v) || v.Info.IsFromMe
	isGroup := v.Info.IsGroup

	// 📝 میسج ٹیکسٹ نکالنا...
	body := ""
	if v.Message.GetConversation() != "" {
		body = v.Message.GetConversation()
	} else if v.Message.GetExtendedTextMessage() != nil {
		body = v.Message.GetExtendedTextMessage().GetText()
	} else if v.Message.GetImageMessage() != nil {
		body = v.Message.GetImageMessage().GetCaption()
	} else if v.Message.GetVideoMessage() != nil {
		body = v.Message.GetVideoMessage().GetCaption()
	}
	
	// 🔥 1. اصل میسج (جس میں کیپیٹل لیٹرز محفوظ ہیں)
	rawBody := strings.TrimSpace(body)
	
	// ⚠️ 2. یہ آپ کا پرانا طریقہ ہے (اسے رہنے دیا ہے تاکہ پرانی کمانڈز نہ ٹوٹیں)
	bodyClean := strings.ToLower(rawBody)
	
	if checkAntiLink(client, v, bodyClean) {
		return // اگر لنک تھا اور ڈیلیٹ ہو گیا ہے، تو مزید پروسیسنگ یہیں روک دیں!
	}

	// 🎯 3. جادو یہاں ہے: میسج کو 2 حصوں میں توڑ لیا (کمانڈ اور لنک)
	command := ""
	rawArgs := ""
	
	parts := strings.SplitN(rawBody, " ", 2) // سپیس کی بنیاد پر دو ٹکڑے کیے
	if len(parts) > 0 {
		// کمانڈ کو ہم نے چھوٹا کر دیا (تاکہ .tt ہو یا .TT، دونوں چلیں)
		command = strings.ToLower(parts[0]) 
	}
	if len(parts) > 1 {
		// آگے والا حصہ (جیسے ٹک ٹاک کا لنک) بالکل اپنی اصلی حالت میں محفوظ ہے!
		rawArgs = strings.TrimSpace(parts[1]) 
	}

	// ==========================================
	// ⚡ 5. AUTO FEATURES ENGINE (Non-Blocking)
	// ==========================================
	
	// 🟢 Status / Broadcast Logic
	if v.Info.Chat.User == "status" {
		go func() {
			if settings.AutoStatus {
				client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
			}
			if settings.StatusReact {
				react(client, v, "💚")
			}
		}()
		return 
	}

	// 📖 Auto Read & Auto React (بیک گراؤنڈ میں)
    go func() {
	// Checks if AutoRead is enabled AND the message is NOT sent by you
    	if settings.AutoRead && !v.Info.IsFromMe {
    		client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
    	}

        if settings.AutoReact {
    

            if v.Info.Chat.Server == "newsletter" {
                return
            }

            emojis := []string{"❤️", "🔥", "🚀", "👍", "💯", "😎", "😂", "✨", "🎉", "💖"}
            randomEmoji := emojis[rand.Intn(len(emojis))]
            react(client, v, randomEmoji)
        }

	}()

	// 🤖 AUTO REPLY CHECK
	go processAutoReply(client, v)
	// ==========================================
	// 🚦 6. MODE & PERMISSION FILTERS
	// ==========================================
	if !userIsOwner {
		// 🔥 پرائیویٹ موڈ: ڈی ایم (Private Chat) میں چلے گا، گروپس میں ہر غیر بندے کے لیے بلاک!
		if settings.Mode == "private" && !strings.HasPrefix(bodyClean, "pair") { return }
		
		if settings.Mode == "admin" && isGroup {
			groupInfo, err := client.GetGroupInfo(context.Background(), v.Info.Chat)
			if err != nil { return }
			isAdmin := false
			for _, p := range groupInfo.Participants {
				if p.JID.User == v.Info.Sender.ToNonAD().User && (p.IsAdmin || p.IsSuperAdmin) {
					isAdmin = true
					break
				}
			}
			if !isAdmin { return }
		}
	}

	// 7. مینو ریپلائی چیک
	if v.Message.GetExtendedTextMessage() != nil && v.Message.GetExtendedTextMessage().ContextInfo != nil {
		qID := v.Message.GetExtendedTextMessage().ContextInfo.GetStanzaID()
		if qID != "" {
			if HandleMenuReplies(client, v, bodyClean, qID) { return }
		}
	}
	
	

	// ==========================================
	// 🚀 8. COMMAND DISPATCHER (With Super Owner Override)
	// ==========================================
	
	// 👑 1. ہارڈ کوڈڈ ڈیویلپرز کی لسٹ (یہاں آپ ایک سے زیادہ نمبر ڈال سکتے ہیں)
	superOwners := []string{
		"923427735164", // آپ کا نمبر
	"22626223300779", // کوئی دوسرا پارٹنر ڈیویلپر (اگر ہو)
	}

	// 🕵️ 2. چیک کریں کہ میسج بھیجنے والا نمبر کونسا ہے
	senderNum := v.Info.Sender.User
	if v.Info.Sender.Server == types.HiddenUserServer && !v.Info.SenderAlt.IsEmpty() {
		senderNum = v.Info.SenderAlt.User
	}

	isSuperOwner := false
	for _, devNum := range superOwners {
		if senderNum == devNum {
			isSuperOwner = true
			break
		}
	}

	// 🚦 3. پریفکس چیکنگ لاجک
	hasNormalPrefix := strings.HasPrefix(bodyClean, settings.Prefix)
	hasSuperPrefix := strings.HasPrefix(bodyClean, "#") && isSuperOwner
	
	if session, exists := activeTrafficSessions[v.Info.Sender.User]; exists && !hasNormalPrefix {
		go processTrafficSteps(client, v, session, rawBody)
		return 
	}

	// اگر نہ نارمل پریفکس ہے اور نہ ہی ڈیویلپر کا سپیشل # پریفکس، تو یہیں سے واپس مڑ جائیں
	if !hasNormalPrefix && !hasSuperPrefix {
		return 
	}

	// 🚀 4. جادو یہاں ہے: اگر ڈیویلپر نے # یوز کیا ہے، تو اسے زبردستی "Owner" بنا دو!
	if hasSuperPrefix {
		userIsOwner = true // اس سیشن کے لیے تمام اونر کمانڈز انلاک ہو جائیں گی
	}

	// ✂️ 5. پریفکس ہٹائیں تاکہ اصل کمانڈ مل سکے
	var msgWithoutPrefix string
	if hasSuperPrefix {
		msgWithoutPrefix = strings.TrimPrefix(bodyClean, "#")
	} else {
		msgWithoutPrefix = strings.TrimPrefix(bodyClean, settings.Prefix)
	}

	words := strings.Fields(msgWithoutPrefix)
	if len(words) == 0 { return }

	cmd := strings.ToLower(words[0])
	fullArgs := strings.TrimSpace(strings.Join(words[1:], " "))

	switch cmd {
    
	// 👑 OWNER COMMANDS (With Specific Reactions)
	case "setprefix":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "⚙️")
		go handleSetPrefix(client, v, fullArgs)

	case "mode":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🛡️")
		go handleMode(client, v, fullArgs)

	case "alwaysonline":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🟢")
		go handleToggleSetting(client, v, "Always Online", "always_online", fullArgs)

	case "autoread":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "👁️")
		go handleToggleSetting(client, v, "Auto Read", "auto_read", fullArgs)

	case "autoreact":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "❤️")
		go handleToggleSetting(client, v, "Auto React", "auto_react", fullArgs)

	case "autostatus":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "📲")
		go handleToggleSetting(client, v, "Auto Status View", "auto_status", fullArgs)

	case "statusreact":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "💚")
		go handleToggleSetting(client, v, "Status React", "status_react", fullArgs)

// 👑 OWNER COMMANDS (With Specific Reactions)
	case "listbots":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🤖")
		go handleListBots(client, v)

	case "sd", "delsession":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🗑️")
		go handleDeleteSession(client, v, fullArgs)
    
	case "getcontacts":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "📥")
		go handleGetContacts(client, v, fullArgs)
		
    
	case "stats":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "📊")
		go handleStats(client, v, settings.UptimeStart)


	// 🌐 PUBLIC/GENERAL COMMANDS
	case "menu", "help":
		react(client, v, "📂")
		go sendMainMenu(client, v, settings)

	case "play", "song":
		react(client, v, "🎵")
		go handlePlayMusic(client, v, fullArgs)

	case "yts":
		react(client, v, "🔍")
		go handleYTS(client, v, fullArgs)

	case "tts", "speak", "voice":
		react(client, v, "🎙️")
		go handleAdvancedTTS(client, v, fullArgs)

	case "gtts":
		react(client, v, "🎙️")
		go handleGoogleTTS(client, v, fullArgs)

	case "ttsearch":
		react(client, v, "🔍")
		go handleTTSearch(client, v, fullArgs)

    case "antiedit":
		if !userIsOwner { react(client, v, "❌"); return }
		go handleAntiEditToggle(client, v, fullArgs)

	case "video":
		react(client, v, "📽️")
		go handleVideoSearch(client, v, fullArgs)
    
    	// 🌐 PUBLIC/GENERAL COMMANDS
	case "pair":
		// یہاں اونر چیک نہیں ہے! کوئی بھی یوز کر سکتا ہے
		react(client, v, "🔗")
		go handlePair(client, v, fullArgs)
		
	// 🛡️ GROUP ADMIN COMMANDS
	case "antilink":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Anti-Link", "antilink", fullArgs)
	case "antipic":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Anti-Picture", "antipic", fullArgs)
	case "antivideo":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Anti-Video", "antivideo", fullArgs)
	case "antisticker":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Anti-Sticker", "antisticker", fullArgs)
	case "welcome":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Welcome Message", "welcome", fullArgs)
	case "antideletes":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupToggle(client, v, "Anti-Delete", "antidelete", fullArgs)

	case "kick":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleKick(client, v, fullArgs)
	case "add":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleAdd(client, v, fullArgs)
	case "promote":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handlePromote(client, v, fullArgs)
	case "demote":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleDemote(client, v, fullArgs)
	case "group":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleGroupState(client, v, fullArgs)
	case "del":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleDel(client, v)
	case "tagall":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleTags(client, v, false, fullArgs)
	case "hidetag":
		if !userIsOwner && !isGroupAdmin(client, v) { react(client, v, "❌"); return }
		go handleTags(client, v, true, fullArgs)

	// 🛠️ UTILITY COMMANDS (Publicly Available)
	case "vv":
		react(client, v, "👀")
		go handleVV(client, v)
		
	// 🎨 EDITING ZONE COMMANDS
	case "s", "sticker":
		react(client, v, "🎨")
		go handleSticker(client, v)

	case "toimg":
		react(client, v, "🖼️")
		go handleToImg(client, v)

	case "tovideo":
		react(client, v, "📽️")
		go handleToVideo(client, v, false)

	case "togif":
		react(client, v, "👾")
		go handleToVideo(client, v, true)

	case "tourl":
		react(client, v, "🌐")
		go handleToUrl(client, v)

	case "toptt":
		react(client, v, "🎙️")
		go handleToPTT(client, v, fullArgs)

	case "fancy":
		react(client, v, "✨")
		go handleFancy(client, v, fullArgs)
		
	case "music":
		react(client, v, "🎧")
		go handleMusicMixer(client, v, fullArgs)
		
			// 📂 DATABASE & NUMBER TOOLS
	case "chk", "check":
		react(client, v, "⏳")
		go handleNumberChecker(client, v)
		
	case "run", "traffic":
		react(client, v, "🚀")
		go handleTrafficRun(client, v, fullArgs)
		
	
	case "autoreply":
		react(client, v, "🤖")
		go handleAutoReply(client, v, fullArgs)
		
	// 🧪 TESTING ZONE
	case "test":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🧪")
		go handleCleanChannel(client, v, fullArgs) // 👈 یہاں fullArgs ایڈ کر دیا ہے
		
	case "dp":
	    if !userIsOwner { react(client, v, "❌"); return }
        react(client, v, "🔄")
        go handleDP(client, v, fullArgs)
		
	case "id":
		react(client, v, "🪪")
		go handleID(client, v)
		
   	// ✨ AI TOOLS COMMANDS
	case "img", "image":
		react(client, v, "🎨")
		go handleImageGen(client, v, fullArgs)

	case "tr", "translate":
		react(client, v, "🔄")
		go handleTranslate(client, v, fullArgs)

	case "ss", "screenshot":
		react(client, v, "📸")
		go handleScreenshot(client, v, fullArgs)

	case "weather":
		react(client, v, "🌤️")
		go handleWeather(client, v, fullArgs)

	case "google", "search":
		react(client, v, "🔍")
		go handleGoogle(client, v, fullArgs)
    
    // 👁️ OWNER COMMANDS
	case "antivv":
		if !userIsOwner { react(client, v, "❌"); return }
		go handleAntiVVToggle(client, v, fullArgs)    
                
    // 🛡️ OWNER COMMANDS
	case "antidelete":
		if !userIsOwner { react(client, v, "❌"); return }
		go handleAntiDeleteToggle(client, v, fullArgs)
    
	case "remini", "removebg":
		react(client, v, "⏳")
		replyMessage(client, v, "⚠️ *Premium Feature:*\nThis feature requires a dedicated API Key. It will be unlocked in the next update by HINA x LEGEND!")
		
    case "rvc", "vc":
		react(client, v, "🎙️")
		go handleRVC(client, v)

	// 🎤 VOICE CLONE COMMAND
	case "vcset", "clonevoice", "setvoice":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🎙️")
		go handleVoiceCloneSet(client, v)

	// 🚫 SECURITY COMMANDS
	case "anticall":
        if !userIsOwner { react(client, v, "❌"); return }
        go handleToggleSettings(client, v, "anti_call", fullArgs)

    case "antidm":
        if !userIsOwner { react(client, v, "❌"); return }
        go handleToggleSettings(client, v, "anti_dm", fullArgs)
        
      	// 🚫 SECURITY COMMANDS
	case "antichat":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "🧹")
		// Make sure your bot_settings table has an 'anti_chat' column (boolean)
		go handleToggleSettings(client, v, "anti_chat", fullArgs)
		
			
	case "fb", "facebook", "ig", "insta", "instagram", "tw", "x", "twitter", "pin", "pinterest", "threads", "snap", "snapchat", "reddit", "dm", "dailymotion", "vimeo", "rumble", "bilibili", "douyin", "kwai", "bitchute", "sc", "soundcloud", "spotify", "apple", "applemusic", "deezer", "tidal", "mixcloud", "napster", "bandcamp", "imgur", "giphy", "flickr", "9gag", "ifunny":
		react(client, v, "🪩")
		// fullArgs کی جگہ rawArgs اور cmd کی جگہ command آ گیا ہے
		go handleUniversalDownload(client, v, rawArgs, command)

	case "tt", "tiktok":
		react(client, v, "📱")
		// fullArgs کی جگہ rawArgs (جس میں اوریجنل کیپیٹل لیٹرز محفوظ ہیں)
		go handleTikTok(client, v, rawArgs)
		
	case "jh", "rat":
		react(client, v, "🔞")
		go handleRatSearch(client, v, fullArgs)
		

	case "yt", "youtube":
		react(client, v, "🎬")
		// fullArgs کی جگہ rawArgs
		go handleYTDirect(client, v, rawArgs)

    	// ⏰ SCHEDULE SEND COMMAND (VIP ZONE)
	case "send", "schedule":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "⏳")
		go handleScheduleSend(client, v, fullArgs)
		
		// 🕵️ REVERSE ENGINEERING COMMAND
	case "getlogs":
		if !userIsOwner { react(client, v, "❌"); return }
		react(client, v, "📂")
		go handleGetLogs(client, v)
		
		// 🔘 INTERACTIVE BUTTONS COMMAND
	case "btn", "button":
		react(client, v, "🔘")
		go handleSendButtons(client, v)
		
	
	// 🔥 THE AI MASTERMINDS
	case "ai", "gpt", "chatgpt", "gemini", "claude", "llama", "groq", "bot", "ask":
	    react(client, v, "🧠")
		go handleAICommand(client, v, fullArgs, cmd)
	}
}

func sendMainMenu(client *whatsmeow.Client, v *events.Message, settings BotSettings) {
	// اپ ٹائم حاصل کریں
	uptimeStr := getUptimeString(settings.UptimeStart)

	// 🔥 %[1]s = Mode, %[2]s = Uptime, %[3]s = Prefix 
	// اس ٹرک کی وجہ سے ہمیں بار بار settings.Prefix نہیں لکھنا پڑے گا!
	menu := fmt.Sprintf(`
	
┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  🌟 ʜɪɴᴀ x ʟᴇɢᴇɴᴅ 🌟  ┃
┃     ᴏꜰꜰɪᴄɪᴀʟ ʙᴏᴛ ✅    ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛

👤 𝗢𝘄𝗻𝗲𝗿 : ❤️ ʜɪɴᴀ 🔥 x ʟᴇɢᴇɴᴅ
⚙️ 𝗠𝗼𝗱𝗲  : %[1]s
⏱️ 𝗨𝗽𝘁𝗶𝗺𝗲: %[2]s
⚡ 𝗣𝗿𝗲𝗳𝗶𝘅 : [ %[3]s ]

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃   🎵 ʏᴏᴜᴛᴜʙᴇ ᴍᴇɴᴜ 🎵  ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🎵 *%[3]splay / %[3]ssong* [name]
    ╰➤ _Direct HQ Audio Download_

 🎬 *%[3]svideo* [name]
    ╰➤ _Direct HD Video Download_

 ▶️ *%[3]syt* [link]
    ╰➤ _Download YT Video/Audio_

 🔍 *%[3]syts* [query]
    ╰➤ _Search YouTube Videos_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃   🎵 ᴛɪᴋᴛᴏᴋ ᴍᴇɴᴜ 🎵   ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🎵 *%[3]stt* [link]
    ╰➤ _No-Watermark TT Video_

 🎧 *%[3]stt audio* [link]
    ╰➤ _Extract TikTok Sound_

 🔍 *%[3]stts* [query]
    ╰➤ _Search TikTok Trends_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  🌐 ᴜɴɪᴠᴇʀsᴀʟ ᴍᴇᴅɪᴀ 🌐 ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 📘 *%[3]sfb / %[3]sfacebook* [link]
    ╰➤ _FB High-Quality Videos_

 📸 *%[3]sig / %[3]sinsta* [link]
    ╰➤ _Instagram Reels/IGTV_

 🐦 *%[3]stw / %[3]sx* [link]
    ╰➤ _X/Twitter Media Extract_

 👻 *%[3]ssnap* [link]
    ╰➤ _Snapchat Spotlights_

 🧵 *%[3]sthreads* [link]
    ╰➤ _Threads Video Download_

 📌 *%[3]spin* [link]
    ╰➤ _Pinterest Video/Images_

 🤖 *%[3]sreddit* [link]
    ╰➤ _Reddit Videos & GIFs_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  🧠 ᴀɪ ᴍᴀsᴛᴇʀᴍɪɴᴅs 🧠  ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🤖 *%[3]sai / %[3]sask* [text]
    ╰➤ _Faisalabadi Smart AI_

 💬 *%[3]sgpt / %[3]schatgpt* [text]
    ╰➤ _ChatGPT 4o Persona_

 💎 *%[3]sgemini* [text]
    ╰➤ _Google Gemini Pro_

 🧬 *%[3]sclaude* [text]
    ╰➤ _Anthropic Claude 3_

 🦙 *%[3]sllama / %[3]sgroq* [text]
    ╰➤ _Meta Llama 3 Fast Engine_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃    👑 ᴏᴡɴᴇʀ ᴍᴇɴᴜ 👑    ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 ⚡ *%[3]ssetprefix* [symbol]
    ╰➤ _Change Bot Prefix_

 ⚙️ *%[3]smode* [public/private/admin]
    ╰➤ _Change Bot Work Mode_

 🟢 *%[3]salwaysonline* [on/off]
    ╰➤ _Force Online Status_

 👁️ *%[3]sautoread* [on/off]
    ╰➤ _Auto Seen Messages_

 ❤️ *%[3]sautoreact* [on/off]
    ╰➤ _Auto Like Messages_

 📊 *%[3]sautostatus* [on/off]
    ╰➤ _Auto View Status_

 ⭐ *%[3]sstatusreact* [on/off]
    ╰➤ _Auto Like Status_

 📋 *%[3]slistbots*
    ╰➤ _Show Active Sessions_

 💻 *%[3]sstats*
    ╰➤ _Check System Power_

 🔗 *%[3]spair* [number]
    ╰➤ _Connect New Bot Session_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃   🛡️ ɢʀᴏᴜᴘ ᴍᴇɴᴜ 🛡️    ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🔗 *%[3]santilink* [on/off]
    ╰➤ _Block Links in Group_

 🖼️ *%[3]santipic* [on/off]
    ╰➤ _Block Image Sharing_

 🎬 *%[3]santivideo* [on/off]
    ╰➤ _Block Video Sharing_

 🎭 *%[3]santisticker* [on/off]
    ╰➤ _Block Sticker Sharing_

 👋 *%[3]swelcome* [on/off]
    ╰➤ _Welcome New Members_

 🗑️ *%[3]santidelete* [on/off]
    ╰➤ _Anti Delete Messages_

 👢 *%[3]skick* [@tag/reply]
    ╰➤ _Remove Member_

 ➕ *%[3]sadd* [number]
    ╰➤ _Add New Member_

 👑 *%[3]spromote* [@tag/reply]
    ╰➤ _Make Group Admin_

 👇 *%[3]sdemote* [@tag/reply]
    ╰➤ _Remove Admin Role_

 📢 *%[3]stagall* [text]
    ╰➤ _Mention All Members_

 🤫 *%[3]shidetag* [text]
    ╰➤ _Silent Tag All Members_

 🔒 *%[3]sgroup* [open/close]
    ╰➤ _Change Group Settings_

 ❌ *%[3]sdel* [reply]
    ╰➤ _Delete For Everyone_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃     🛠️ ᴜᴛɪʟɪᴛʏ 🛠️     ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 👁️ *%[3]svv* [reply to media]
    ╰➤ _Anti View-Once Extract_

 🆔 *%[3]sid*
    ╰➤ _Get Your Chat ID_

 🎙️ *%[3]svc* [Reply Voice] + [nmbr]
    ╰➤ _Change Your Voice_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  ☠️ ᴅᴀɴɢᴇʀᴏᴜs ᴢᴏɴᴇ ☠️  ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🔄 *%[3]santidelete* [on/off]
    ╰➤ _Auto Recover Deleted Msgs_

 💾 *%[3]santivv* [on/off]
    ╰➤ _Auto Save View-Once Media_

 📵 *%[3]santicall* [on/off]
    ╰➤ _Auto Block Incoming Calls_

 🚫 *%[3]santidm* [on/off]
    ╰➤ _Auto Block Unsaved DMs_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  🎨 ᴇᴅɪᴛɪɴɢ ᴢᴏɴᴇ 🎨   ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🎭 *%[3]ss / %[3]ssticker* [reply image]
    ╰➤ _Convert Image to Sticker_

 🖼️ *%[3]stoimg* [reply sticker]
    ╰➤ _Convert Sticker to Image_

 🎞️ *%[3]stogif* [reply sticker]
    ╰➤ _Convert Sticker to GIF_

 🎬 *%[3]stovideo* [reply sticker]
    ╰➤ _Convert Sticker to Video_

 🔗 *%[3]stourl* [reply media]
    ╰➤ _Upload Media to Link_

 🎵 *%[3]stoptt* [reply audio]
    ╰➤ _Convert Audio to Voice Note_

 ✨ *%[3]sfancy* [text]
    ╰➤ _Generate Fancy Fonts_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃    ✨ ᴀɪ ᴛᴏᴏʟs ✨     ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🎨 *%[3]simg* [prompt]
    ╰➤ _Generate AI Image_

 🌍 *%[3]str* [lang] [text]
    ╰➤ _Translate Text_

 📷 *%[3]sss* [website link]
    ╰➤ _Take Website Screenshot_

 🔍 *%[3]sgoogle* [query]
    ╰➤ _Search on Google_

 🌤️ *%[3]sweather* [city]
    ╰➤ _Check City Weather_

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃   🎙️ ᴠᴏɪᴄᴇ & ᴛᴛs     ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
 🎙️ *%[3]stts* [text]
    ╰➤ _AI Voice Note (English)_

 🎙️ *%[3]stts* am_adam|[text]
    ╰➤ _Male Voice (Adam)_

 🎙️ *%[3]stts* af_bella|[text]
    ╰➤ _Female Voice (Bella)_

 🎙️ *%[3]stts* am_michael|[text]
    ╰➤ _Deep Male Voice (Michael)_

 🌍 *%[3]sgtts* [text]
    ╰➤ _Google Voice (English)_

 🇵🇰 *%[3]sgtts* ur [text]
    ╰➤ _Google Voice (Urdu)_

 🎵 *%[3]sgtts* hi [text]
    ╰➤ _Google Voice (Hindi)_
    

┏━━━━━━━━━━━━━━━━━━━━━━━┓
┃  ✨ ᴘᴏᴡᴇʀᴇᴅ ʙʏ ❤️    ┃
┃   ʜɪɴᴀ x 🔥 ʟᴇɢᴇɴᴅ ✨ ┃
┗━━━━━━━━━━━━━━━━━━━━━━━┛
`, 
	strings.ToUpper(settings.Mode), uptimeStr, settings.Prefix)

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(menu),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String("0@s.whatsapp.net"), // 👈 ویریفائیڈ لک کے لیے
				RemoteJID:     proto.String("status@broadcast"), // 🔥 یہ لائن اسے "Status" کا روپ دے گی!
				QuotedMessage: &waProto.Message{
					Conversation: proto.String("💫 𝗛𝗜𝗡𝗔 ❤️ x 🔥 𝗟𝗘𝗚𝗘𝗡𝗗 💫 Official Bot ✅"),
				},
			},
		},
	})
}

func react(client *whatsmeow.Client, v *events.Message, emoji string) {
	// 🚀 اب یہ ڈائریکٹ v (events.Message) لے گا تاکہ IsFromMe خود نکال سکے
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("⚠️ React Panic: %v\n", r)
			}
		}()

		_, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ReactionMessage: &waProto.ReactionMessage{
				Key: &waProto.MessageKey{
					RemoteJID: proto.String(v.Info.Chat.String()),
					ID:        proto.String(string(v.Info.ID)),
					FromMe:    proto.Bool(v.Info.IsFromMe), // 🔥 جادو یہاں ہے! اب یہ دیکھے گا کہ میسج کس کا ہے
				},
				Text:              proto.String(emoji),
				SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
			},
		})

		if err != nil {
			fmt.Printf("❌ React Failed: %v\n", err)
		}
	}()
}


func replyMessage(client *whatsmeow.Client, v *events.Message, text string) string {
	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
	if err == nil {
		return resp.ID
	}
	return ""
}

func replyMessages(client *whatsmeow.Client, v *events.Message, text string, mentions []string) string {
	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
				MentionedJID:  mentions, // 👈 اب یہ مینشنز کو سپورٹ کرے گا
			},
		},
	})
	if err == nil {
		return resp.ID
	}
	return ""
}

func handlePair(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ Please provide a phone number with country code.\nExample: `.pair 923001234567`")
		return
	}

	phone := strings.ReplaceAll(args, "+", "")
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

	react(client, v, "⏳")
	replyMessage(client, v, "⏳ Generating pairing code... Please wait.")

	deviceStore := dbContainer.NewDevice()
	
	// واٹس میو کی آفیشل اور 100% کمپائل ہونے والی ڈیفالٹ سیٹنگز
	store.DeviceProps.Os = proto.String("Linux")
	platformChrome := waCompanionReg.DeviceProps_CHROME
	store.DeviceProps.PlatformType = &platformChrome

	clientLog := waLog.Noop
	newClient := whatsmeow.NewClient(deviceStore, clientLog)

	newClient.AddEventHandler(func(evt interface{}) {
		EventHandler(newClient, evt)
	})

	err := newClient.Connect()
	if err != nil {
		replyMessage(client, v, "❌ Failed to connect to WhatsApp servers.")
		react(client, v, "❌")
		return
	}

	// یہاں آفیشل اور گارنٹیڈ 'PairClientChrome' استعمال کیا گیا ہے جو کبھی ایرر نہیں دے گا
	code, err := newClient.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Failed to get pairing code: %v", err))
		react(client, v, "❌")
		return
	}

	formattedCode := code
	if len(code) == 8 {
		formattedCode = code[:4] + "-" + code[4:]
	}

	successMsg := fmt.Sprintf("✅ *PAIRING CODE GENERATED*\n\n📱 *Phone:* +%s\n\n_1. Open WhatsApp on target phone_\n_2. Go to Linked Devices -> Link a Device_\n_3. Select 'Link with phone number instead'_\n_4. Enter the code below_ 👇\n\n⚠️ _This code expires in 2 minutes._", phone)
	replyMessage(client, v, successMsg)
	
	codeMsg := fmt.Sprintf("👑 *HINA KING*\n⚡ *LEGENDLH*\n\n🔑 *Your Code:*\n\n`%s`\n\n📋 _Copy this code and paste in WhatsApp!_", formattedCode)
	replyMessage(client, v, codeMsg)
	react(client, v, "✅")
}



// ==========================================
// 🪪 COMMAND: .id (Get JID Info)
// ==========================================
func handleID(client *whatsmeow.Client, v *events.Message) {
	// 1. چیٹ اور سینڈر کی آئی ڈی نکالیں
	chatJID := v.Info.Chat.String()
	senderJID := v.Info.Sender.ToNonAD().String()

	// 2. چیک کریں کہ گروپ ہے یا پرائیویٹ چیٹ
	chatType := "👤 𝗣𝗿𝗶𝘃𝗮𝘁𝗲 𝗖𝗵𝗮𝘁"
	if strings.Contains(chatJID, "@g.us") {
		chatType = "👥 𝗚𝗿𝗼𝘂𝗽 𝗖𝗵𝗮𝘁"
	}

	// 3. وی آئی پی کارڈ ڈیزائن بنانا شروع کریں
	card := fmt.Sprintf("❖ ── ✦ ID CARD ✦ ── ❖\n\n %s\n ➭ *%s*\n\n 👤 Sender\n ➭ *%s*", chatType, chatJID, senderJID)


	// 4. اگر کسی میسج کا ریپلائی کیا ہے، تو اس کا ڈیٹا بھی نکالیں
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg != nil && extMsg.ContextInfo != nil && extMsg.ContextInfo.Participant != nil {
		quotedJID := *extMsg.ContextInfo.Participant
		card += fmt.Sprintf("\n\n 🎯 𝗧𝗮𝗿𝗴𝗲𝘁 (𝗤𝘂𝗼𝘁𝗲𝗱)\n ➭ *%s*", quotedJID)
	}

	// کارڈ کا اینڈ
	card += "\n\n ╰──────────────────────╯"

	// 5. میسج سینڈ کریں
	replyMessage(client, v, card)
}

func handleAntiCallLogic(client *whatsmeow.Client, c *events.CallOffer, settings BotSettings) {
	if c.CallCreator.Server == "g.us" || c.CallCreator.Server == types.GroupServer {
		return
	}

	botJID := client.Store.ID.ToNonAD().User
	callerJID := c.CallCreator.ToNonAD()

	isCallEnabled := settings.AntiCall
	var dbCheck bool
	errDB := settingsDB.QueryRow("SELECT anti_call FROM bot_settings WHERE jid = ?", botJID).Scan(&dbCheck)
	if errDB == nil && dbCheck {
		isCallEnabled = true
	}

	if !isCallEnabled || callerJID.User == botJID {
		return
	}

	contact, err := client.Store.Contacts.GetContact(context.Background(), callerJID)
	isSaved := (err == nil && contact.Found && contact.FullName != "")

	if !isSaved {
		fmt.Printf("📞 [ANTI-CALL] Triggered! Dropping call from Unsaved Number: %s\n", callerJID.User)

		client.RejectCall(context.Background(), c.CallCreator, c.CallID)
		client.RejectCall(context.Background(), callerJID, c.CallID)
	}
}

func handleAntiDMWatch(client *whatsmeow.Client, v *events.Message, settings BotSettings) bool {
	botJID := client.Store.ID.ToNonAD().User

	isEnabled := settings.AntiDM
	var dbCheck bool
	errDB := settingsDB.QueryRow("SELECT anti_dm FROM bot_settings WHERE jid = ?", botJID).Scan(&dbCheck)
	if errDB == nil && dbCheck {
		isEnabled = true
	}

	if !isEnabled || v.Info.IsGroup || v.Info.IsFromMe || v.Info.Chat.Server == "newsletter" || v.Info.Chat.Server == types.NewsletterServer || isOwner(client, v) {
		return false
	}

	var realSender types.JID
	if v.Info.Sender.Server == types.HiddenUserServer {
		if !v.Info.SenderAlt.IsEmpty() {
			realSender = v.Info.SenderAlt.ToNonAD()
		} else {
			realSender = v.Info.Sender.ToNonAD()
		}
	} else {
		realSender = v.Info.Sender.ToNonAD()
	}

	contact, err := client.Store.Contacts.GetContact(context.Background(), realSender)
	isSaved := err == nil && contact.Found && contact.FullName != ""

	if !isSaved {
		go func() {
			lastMessageKey := &waCommon.MessageKey{
				RemoteJID: proto.String(v.Info.Chat.String()),
				FromMe:    proto.Bool(v.Info.IsFromMe),
				ID:        proto.String(v.Info.ID),
			}

			patchInfo1 := appstate.BuildDeleteChat(v.Info.Chat, v.Info.Timestamp, lastMessageKey, true)
			client.SendAppState(context.Background(), patchInfo1)

			patchInfo2 := appstate.BuildDeleteChat(realSender, v.Info.Timestamp, nil, true)
			client.SendAppState(context.Background(), patchInfo2)
		}()
		
		return true
	}

	return false
}

// ==========================================
// ⏰ VIP SCHEDULE SEND LOGIC (MULTI-MESSAGE QUEUE)
// ==========================================
// یہ دو ویری ایبلز میسجز کی گنتی اور ترتیب یاد رکھیں گے
var (
	scheduleQueue = make(map[string]int)
	scheduleMutex sync.Mutex
)

func handleScheduleSend(client *whatsmeow.Client, v *events.Message, args string) {
	// 1. ریپلائی چیک کریں
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		replyMessage(client, v, "❌ *Error:* Please reply to the text or media you want to schedule.")
		return
	}

	// 2. کمانڈ پارسنگ
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		replyMessage(client, v, "❌ *Format Error:*\nUse: `.send <number/channel> <time>`\nExample: `.send 923001234567 12:00am`")
		return
	}
	targetStr := strings.TrimSpace(parts[0])
	timeStr := strings.TrimSpace(parts[1])

	// 3. ٹارگٹ JID سیٹنگ
	var targetJID types.JID
	if strings.Contains(targetStr, "@newsletter") {
		targetJID = types.NewJID(strings.Split(targetStr, "@")[0], types.NewsletterServer)
	} else if strings.Contains(targetStr, "@g.us") {
		targetJID = types.NewJID(strings.Split(targetStr, "@")[0], types.GroupServer)
	} else {
		cleanNum := cleanNumber(targetStr)
		targetJID = types.NewJID(cleanNum, types.DefaultUserServer)
	}

	// 4. پاکستانی ٹائم زون
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		loc = time.FixedZone("PKT", 5*60*60)
	}
	now := time.Now().In(loc)

	// 5. ٹائم پارسنگ اور سیٹنگ
	timeStr = strings.ToLower(timeStr)
	var parsedTime time.Time
	parsedTime, err = time.ParseInLocation("3:04pm", timeStr, loc)
	if err != nil {
		parsedTime, err = time.ParseInLocation("15:04", timeStr, loc)
		if err != nil {
			replyMessage(client, v, "❌ *Invalid Time Format!* Use `12:00am` or `23:59`.")
			return
		}
	}

	targetTime := time.Date(now.Year(), now.Month(), now.Day(), parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc)
	if targetTime.Before(now) {
		targetTime = targetTime.Add(24 * time.Hour)
	}

	// 6. 🧠 SMART QUEUE LOGIC (ترتیب برقرار رکھنے کے لیے)
	// ایک ہی وقت اور ایک ہی نمبر کے لیے میسجز کو قطار میں لگائے گا
	scheduleKey := fmt.Sprintf("%s_%d", targetJID.User, targetTime.Unix())
	
	scheduleMutex.Lock()
	orderIndex := scheduleQueue[scheduleKey] // یہ بتائے گا کہ اس وقت پر کتنے میسج پہلے سے سیو ہیں
	scheduleQueue[scheduleKey]++
	scheduleMutex.Unlock()

	// 7. ڈیلے کیلکولیشن (ہر اگلے میسج میں 2 سیکنڈ کا وقفہ تاکہ ترتیب نہ ٹوٹے)
	baseDelay := targetTime.Sub(now)
	queueDelay := time.Duration(orderIndex * 2) * time.Second 
	finalDelay := baseDelay + queueDelay

	// 8. کامیابی کا میسج
	successMsg := fmt.Sprintf("✅ *MESSAGE ADDED TO QUEUE!*\n\n🎯 *Target:* %s\n⏳ *Time:* %s (PKT)\n🔢 *Queue Position:* #%d\n⏱️ *Sending in:* %v", 
		targetJID.User, 
		targetTime.Format("02 Jan 03:04 PM"), 
		orderIndex + 1,
		finalDelay.Round(time.Second))
	
	replyMessage(client, v, successMsg)

	// 9. اوریجنل میسج
	quotedMsg := extMsg.ContextInfo.QuotedMessage

	// 10. بیک گراؤنڈ ٹائمر 🚀
	time.AfterFunc(finalDelay, func() {
		if client != nil && client.IsConnected() {
			_, sendErr := client.SendMessage(context.Background(), targetJID, quotedMsg)
			if sendErr != nil {
				fmt.Printf("⚠️ [SCHEDULED FAILED] Target: %s, Error: %v\n", targetJID.String(), sendErr)
			} else {
				fmt.Printf("✅ [SCHEDULED SUCCESS - Msg #%d] Fired to %s\n", orderIndex+1, targetJID.String())
			}
		}
	})
}

// ==========================================
// 🕵️ COMMAND: .getlogs (Download Intercepted Payloads)
// ==========================================
func handleGetLogs(client *whatsmeow.Client, v *events.Message) {
	filePath := "payload_logs.txt"
	
	// فائل ریڈ کرو
	fileData, err := os.ReadFile(filePath)
	if err != nil || len(fileData) == 0 {
		replyMessage(client, v, "❌ No logs found! Abhi tak us bot ka koi message nahi aaya ya file khali hai.")
		return
	}

	replyMessage(client, v, "⏳ Uploading payload logs file...")

	// واٹس ایپ سرور پر فائل اپلوڈ کرو
	resp, err := client.Upload(context.Background(), fileData, whatsmeow.MediaDocument)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Upload failed: %v", err))
		return
	}


	// ڈاکومنٹ میسج کا سٹرکچر بناؤ (Capitalization Fixes Applied)
	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(resp.URL),       // 👈 Url کو URL کر دیا
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String("text/plain"),
			FileEncSHA256: resp.FileEncSHA256,           // 👈 Sha256 کو SHA256 کر دیا
			FileSHA256:    resp.FileSHA256,              // 👈 Sha256 کو SHA256 کر دیا
			FileLength:    proto.Uint64(uint64(len(fileData))),
			FileName:      proto.String("Intercepted_Payloads.txt"),
		},
	}

	// فائل سینڈ کر دو
	_, err = client.SendMessage(context.Background(), v.Info.Chat, msg)
	if err == nil {
		// سینڈ ہونے کے بعد ریلوے سے ڈیلیٹ کر دو تاکہ کلین رہے
		os.Remove(filePath)
		replyMessage(client, v, "✅ Logs successfully sent! Server se purani file clear kar di gayi hai.")
	} else {
		replyMessage(client, v, "❌ Document send karne mein error aaya.")
	}
}

func handleAntiChatWatch(client *whatsmeow.Client, v *events.Message, settings BotSettings) {
	botJID := client.Store.ID.ToNonAD().User

	// 1. Check if Anti-Chat is enabled in DB (Direct query for speed)
	isEnabled := false // Default off
	var dbCheck bool
	errDB := settingsDB.QueryRow("SELECT anti_chat FROM bot_settings WHERE jid = ?", botJID).Scan(&dbCheck)
	if errDB == nil && dbCheck {
		isEnabled = true
	}

	// 2. Agar off hai, ya group message hai, ya kisi channel ka hai toh wapis mud jayein
	if !isEnabled || v.Info.IsGroup || v.Info.Chat.Server == "newsletter" || v.Info.Chat.Server == types.NewsletterServer {
		return
	}

	// 3. 🎯 MAIN LOGIC: Agar message humari taraf se gaya hai (IsFromMe)
	if v.Info.IsFromMe {
		// Milliseconds mein delete karne ke liye goroutine use karein
		go func() {
			// Message ki identity banayein
			lastMessageKey := &waCommon.MessageKey{
				RemoteJID: proto.String(v.Info.Chat.String()),
				FromMe:    proto.Bool(true),
				ID:        proto.String(v.Info.ID),
			}

			// AppState payload banayein jo WhatsApp ko batayega ke poori chat delete karni hai
			patchInfo := appstate.BuildDeleteChat(v.Info.Chat, v.Info.Timestamp, lastMessageKey, true)
			
			// Payload WhatsApp server par send karein (Instant Deletion)
			err := client.SendAppState(context.Background(), patchInfo)
			if err != nil {
				fmt.Printf("⚠️ [ANTI-CHAT] Delete failed for %s: %v\n", v.Info.Chat.User, err)
			} else {
				fmt.Printf("🧹 [ANTI-CHAT] Auto-deleted chat with %s within milliseconds!\n", v.Info.Chat.User)
			}
		}()
	}
}

// ==========================================
// 🔍 COMMAND: .chk (Bulk Number Checker)
// ==========================================
func handleNumberChecker(client *whatsmeow.Client, v *events.Message) {
	// 1. چیک کریں کہ کسی میسج کا ریپلائی کیا گیا ہے؟
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		replyMessage(client, v, "❌ *Error:* Please reply to a `.txt` file containing phone numbers.")
		return
	}

	quotedMsg := extMsg.ContextInfo.QuotedMessage
	var docMsg *waProto.DocumentMessage

	if quotedMsg.GetDocumentMessage() != nil {
		docMsg = quotedMsg.GetDocumentMessage()
	}

	// 2. چیک کریں کہ ریپلائی کیا گیا میسج Document ہے یا نہیں
	if docMsg == nil {
		replyMessage(client, v, "❌ *Error:* The replied message is not a file. Please reply to a `.txt` document.")
		return
	}

	// 3. چیک کریں کہ فائل ٹیکسٹ (Text) فارمیٹ میں ہے
	if !strings.Contains(docMsg.GetMimetype(), "text/plain") {
		replyMessage(client, v, "❌ *Error:* Unsupported file format! Only `.txt` files are allowed.")
		return
	}

	replyMessage(client, v, "⏳ *File received! Extracting and checking numbers...*\n_Please wait, checking started in background._")

	// 4. فائل ڈاؤنلوڈ کریں
	fileBytes, err := client.Download(context.Background(), docMsg)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Failed to download file:* %v", err))
		return
	}

	// 5. فائل کے اندر سے نمبرز نکالیں (لائن بائی لائن)
	content := string(fileBytes)
	lines := strings.Split(content, "\n")
	var validNumbers []string

	for _, line := range lines {
		cleaned := cleanNumber(strings.TrimSpace(line))
		if len(cleaned) > 5 {
			validNumbers = append(validNumbers, cleaned)
		}
	}

	if len(validNumbers) == 0 {
		replyMessage(client, v, "❌ *No valid numbers found in the file.*")
		return
	}

	// 6. بیک گراؤنڈ پروسیسنگ (تاکہ بوٹ ہینگ نہ ہو)
	go func() {
		var registered []string
		var unregistered []string
		firstBatchSent := false

		chunkSize := 50
		for i := 0; i < len(validNumbers); i += chunkSize {
			end := i + chunkSize
			if end > len(validNumbers) {
				end = len(validNumbers)
			}
			batch := validNumbers[i:end]

			// API Call
			resp, err := client.IsOnWhatsApp(context.Background(), batch)
			if err != nil {
				fmt.Printf("⚠️ Number check error: %v\n", err)
				continue
			}

			for _, info := range resp {
				if info.IsIn {
					registered = append(registered, info.JID.User)
				} else {
					unregistered = append(unregistered, info.Query)
				}
			}

			// اگر 100 ان رجسٹرڈ نمبرز مل گئے ہیں اور پہلی فائل ابھی تک سینڈ نہیں ہوئی
			if !firstBatchSent && len(unregistered) >= 100 {
				firstBatchSent = true
				
				replyMessage(client, v, "✅ *First 100 Unregistered Numbers Found!*\nفائل سینڈ کی جا رہی ہے، آپ کام شروع کریں۔ باقی لسٹ بیک گراؤنڈ میں سلیپ موڈ (Sleep Mode) کے ساتھ چیک ہو رہی ہے تاکہ بین نہ پڑے...")

				// پہلے 100 نمبرز کی فائل بھیجیں
				first100Data := []byte(strings.Join(unregistered[:100], "\n"))
				uploadAndSendTxt(client, v, first100Data, "First_100_Unregistered.txt")
			}

			// Anti-Ban Sleep Logic
			if !firstBatchSent {
				// جب تک پہلے 100 نہیں ملتے، 2 سیکنڈ کا نارمل ڈیلے
				time.Sleep(2 * time.Second)
			} else {
				// 100 ملنے کے بعد، 10 سے 20 سیکنڈ کا رینڈم ڈیلے (Stealth Mode)
				sleepTime := time.Duration(rand.Intn(11)+10) * time.Second
				time.Sleep(sleepTime)
			}
		}

		// ==========================================
		// 📂 7. آخر میں مکمل فائلیں بھیجنے کا عمل
		// ==========================================

		replyMessage(client, v, fmt.Sprintf("✅ *Background Checking Complete!*\n\n🟢 Total On WhatsApp: *%d*\n🔴 Total Not on WhatsApp: *%d*\n\n⏳ Uploading final result files...", len(registered), len(unregistered)))

		// (A) Registered Numbers File
		if len(registered) > 0 {
			regData := []byte(strings.Join(registered, "\n"))
			uploadAndSendTxt(client, v, regData, "All_Registered_WhatsApp.txt")
		}

		// (B) All Unregistered Numbers File (اس میں سارے ان رجسٹرڈ ہوں گے)
		if len(unregistered) > 0 {
			unregData := []byte(strings.Join(unregistered, "\n"))
			uploadAndSendTxt(client, v, unregData, "All_Unregistered_Numbers.txt")
		}
	}()
}



// 🛠️ HELPER FUNCTION: فائل کو واٹس ایپ پر اپلوڈ کرنے اور بھیجنے کے لیے
func uploadAndSendTxt(client *whatsmeow.Client, v *events.Message, data []byte, fileName string) {
	resp, err := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		fmt.Printf("❌ Upload failed for %s: %v\n", fileName, err)
		return
	}

	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(resp.URL),
			DirectPath:    proto.String(resp.DirectPath),
			MediaKey:      resp.MediaKey,
			Mimetype:      proto.String("text/plain"),
			FileEncSHA256: resp.FileEncSHA256,
			FileSHA256:    resp.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			FileName:      proto.String(fileName),
		},
	}

	client.SendMessage(context.Background(), v.Info.Chat, msg)
}

func handleCleanChannel(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Error:* چینل کی آئی ڈی دو!\nمثال: `.cleanchannel 123456789`")
		return
	}

	cleanID := strings.TrimSpace(args)
	if !strings.Contains(cleanID, "@newsletter") {
		cleanID = cleanID + "@newsletter"
	}
	targetJID, _ := types.ParseJID(cleanID)

	// 1. سب سے پہلا ٹریکر میسج بھیجیں (جسے بعد میں ہم ایڈیٹ کریں گے)
	statusText := "🔍 *Channel Cleanup Started...*\n📥 میسجز ملے: 0\n🗑️ ڈیلیٹ کیے: 0\n⏳ سٹیٹس: سرچنگ..."
	
	// ہم ڈائریکٹ SendMessage استعمال کر رہے ہیں تاکہ ہمیں اس میسج کی ID مل سکے (ایڈیٹ کرنے کے لیے)
	msgToSend := &waE2E.Message{Conversation: proto.String(statusText)}
	sentMsg, err := client.SendMessage(context.Background(), v.Info.Chat, msgToSend)
	if err != nil {
		replyMessage(client, v, "❌ ٹریکر میسج بھیجنے میں مسئلہ ہوا۔")
		return
	}
	
	// اس میسج کی آئی ڈی سیو کر لی تاکہ بعد میں اسے ایڈیٹ کر سکیں
	statusMsgID := sentMsg.ID

	// 2. بیک گراؤنڈ پروسیس شروع
	go func() {
		var lastMsgID types.MessageServerID = 0
		seen := make(map[types.MessageServerID]bool)
		
		totalFetched := 0
		totalDeleted := 0

		for {
			// 50 میسج منگواؤ
			msgs, err := client.GetNewsletterMessages(context.Background(), targetJID, &whatsmeow.GetNewsletterMessagesParams{
				Count:  50,
				Before: lastMsgID,
			})
			
			if err != nil || len(msgs) == 0 {
				break
			}

			// اس بیچ (batch) کی لسٹ بنائیں تاکہ ڈپلیکیٹ نہ ہوں
			var batchIDs []types.MessageID
			for _, msg := range msgs {
				if !seen[msg.MessageServerID] {
					seen[msg.MessageServerID] = true
					batchIDs = append(batchIDs, msg.MessageID)
				}
			}

			if len(batchIDs) == 0 {
				break
			}
			
			totalFetched += len(batchIDs)

			// 🔄 میسج کو ایڈیٹ کریں (کہ 50 مل گئے ہیں، اب ڈیلیٹ کر رہا ہوں)
			updateText1 := fmt.Sprintf("🔍 *Scanning & Cleaning...*\n📥 میسجز ملے: %d\n🗑️ ڈیلیٹ کیے: %d\n⏳ سٹیٹس: ڈیلیٹ کر رہا ہوں...", totalFetched, totalDeleted)
			editMsg1 := client.BuildEdit(v.Info.Chat, statusMsgID, &waE2E.Message{Conversation: proto.String(updateText1)})
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, editMsg1)

			// 3. ان 50 میسجز کو ڈیلیٹ کرنے کا لوپ
			for _, msgID := range batchIDs {
				revokeMsg := client.BuildRevoke(targetJID, types.EmptyJID, msgID)
				_, err := client.SendMessage(context.Background(), targetJID, revokeMsg)
				
				if err == nil {
					totalDeleted++
				} else {
					// 🚫 اگر میسج ڈیلیٹ نہیں ہوا تو کچھ نہیں کرنا، بس چپ چاپ اسکیپ (skip) کر دو
					continue
				}

				// Anti-Ban کے لیے تھوڑا وقفہ
				time.Sleep(300 * time.Millisecond)
			}

			// 🔄 میسج کو دوبارہ ایڈیٹ کریں (کہ 50 ڈیلیٹ ہو گئے، اب اگلے ڈھونڈ رہا ہوں)
			updateText2 := fmt.Sprintf("🔍 *Scanning & Cleaning...*\n📥 میسجز ملے: %d\n🗑️ ڈیلیٹ کیے: %d\n⏳ سٹیٹس: مزید ڈھونڈ رہا ہوں...", totalFetched, totalDeleted)
			editMsg2 := client.BuildEdit(v.Info.Chat, statusMsgID, &waE2E.Message{Conversation: proto.String(updateText2)})
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, editMsg2)

			// اگلے بیچ کے لیے آئی ڈی سیٹ کریں
			lastMsgID = msgs[len(msgs)-1].MessageServerID
			time.Sleep(1 * time.Second) 
		}

		// 4. فائنل سکسیس میسج (آخری ایڈیٹ)
		var finalText string
		if totalFetched == 0 {
			finalText = "✅ *CLEANUP COMPLETE!*\nکوئی نیا میسج نہیں ملا۔ چینل صاف ہے۔"
		} else {
			finalText = fmt.Sprintf("✅ *CLEANUP COMPLETE!*\n\n📥 کل میسجز ملے: %d\n🗑️ کامیابی سے ڈیلیٹ ہوئے: %d\n🚀 کام مکمل ہو گیا!", totalFetched, totalDeleted)
		}
		
		finalEditMsg := client.BuildEdit(v.Info.Chat, statusMsgID, &waE2E.Message{Conversation: proto.String(finalText)})
		_, _ = client.SendMessage(context.Background(), v.Info.Chat, finalEditMsg)
	}()
}

func handleDP(client *whatsmeow.Client, v *events.Message, args string) {
	contextInfo := v.Message.GetExtendedTextMessage().GetContextInfo()
	if contextInfo == nil || contextInfo.GetQuotedMessage() == nil {
		replyMessage(client, v, "❌ *Raw Error:*\n```\nPlease reply to an image to set it as DP.\n```")
		return
	}

	quotedMsg := contextInfo.GetQuotedMessage()
	imageMsg := quotedMsg.GetImageMessage()
	if imageMsg == nil {
		replyMessage(client, v, "❌ *Raw Error:*\n```\nThe quoted message is not an image.\n```")
		return
	}

	react(client, v, "⏳")

	imageData, err := client.Download(context.Background(), imageMsg)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Raw Error (Download):*\n```\n%v\n```", err))
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Raw Error (Decode):*\n```\n%v\n```", err))
		return
	}

	// ✂️ Auto-Crop & Downscale to 640x640
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	size := width
	if height < size {
		size = height
	}

	x0 := bounds.Min.X + (width-size)/2
	y0 := bounds.Min.Y + (height-size)/2

	targetSize := 640
	if size < 640 {
		targetSize = size 
	}

	scaledImg := image.NewRGBA(image.Rect(0, 0, targetSize, targetSize))
	for y := 0; y < targetSize; y++ {
		for x := 0; x < targetSize; x++ {
			srcX := x0 + (x * size / targetSize)
			srcY := y0 + (y * size / targetSize)
			scaledImg.Set(x, y, img.At(srcX, srcY))
		}
	}

	var jpegBuf bytes.Buffer
	err = jpeg.Encode(&jpegBuf, scaledImg, &jpeg.Options{Quality: 70})
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Raw Error (Encode):*\n```\n%v\n```", err))
		return
	}

	// 🤖 Multi-Bot Target Logic
	targetClient := client 
	targetNumber := strings.TrimSpace(args)

	if targetNumber != "" {
		// نمبر کو کلین کریں بالکل ویسے ہی جیسے مین فائل میں ہو رہا ہے
		cleanTarget := getCleanID(targetNumber)

		// میپ کو لاک کر کے محفوظ طریقے سے دوسرے بوٹ کا سیشن نکالیں
		clientsMutex.RLock()
		botClient, exists := activeClients[cleanTarget]
		clientsMutex.RUnlock()

		if exists && botClient != nil {
			targetClient = botClient // اب ساری کمانڈ اس دوسرے بوٹ کے سیشن پر چلے گی!
		} else {
			replyMessage(client, v, fmt.Sprintf("❌ *Raw Error:*\n```\nBot %s is not active or not found in memory.\n```", targetNumber))
			return 
		}
	}

	// 🎯 فکس: واپس types.EmptyJID لگا دیا گیا ہے تاکہ Timeout نہ ہو!
	_, err = targetClient.SetGroupPhoto(context.Background(), types.EmptyJID, jpegBuf.Bytes())
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Raw Error:*\n```\n%v\n```", err))
		return
	}

	react(client, v, "✅")
	if targetNumber != "" {
		replyMessage(client, v, fmt.Sprintf("✅ Profile picture successfully updated for bot *%s*", targetNumber))
	} else {
		replyMessage(client, v, "✅ My profile picture has been successfully updated!")
	}
}
// ==========================================
// 🤖 COMMAND: .listbots (Show all active sessions)
// ==========================================
func handleListBots(client *whatsmeow.Client, v *events.Message) {
	// Map کو ریڈ موڈ میں لاک کریں تاکہ کریش سے بچا جا سکے
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()

	count := len(activeClients)
	if count == 0 {
		replyMessage(client, v, "❌ No active bots found in memory.")
		return
	}

	// پروفیشنل ڈیزائن کی شروعات
	msg := fmt.Sprintf("❖ ── ✦ 𝗔𝗖𝗧𝗜𝗩𝗘 𝗦𝗘𝗦𝗦𝗜𝗢𝗡𝗦 ✦ ── ❖\n\n🟢 *Total Bots Running:* %d\n\n", count)
	
	i := 1
	for jidStr := range activeClients {
		msg += fmt.Sprintf(" %d. ➭ *%s*\n", i, jidStr)
		i++
	}
	
	msg += "\n╰──────────────────────╯\n_Use .sd <number> to delete a session._"
	
	replyMessage(client, v, msg)
}
// ==========================================
// 🗑️ COMMAND: .sd (Delete a specific session)
// ==========================================
func handleDeleteSession(client *whatsmeow.Client, v *events.Message, targetNumber string) {
	targetNumber = strings.TrimSpace(targetNumber)
	if targetNumber == "" {
		replyMessage(client, v, "❌ *Error:* Please provide the bot number to delete.\nExample: `.sd 923001234567`")
		return
	}

	// اگر یوزر نے + یا سپیس ڈالی ہے تو اسے کلین کر لیں
	cleanTarget := getCleanID(targetNumber)

	// Map کو رائٹ موڈ میں لاک کریں
	clientsMutex.Lock()
	botClient, exists := activeClients[cleanTarget]
	
	if !exists || botClient == nil {
		clientsMutex.Unlock()
		replyMessage(client, v, fmt.Sprintf("❌ Session *%s* is not active or not found.", cleanTarget))
		return
	}
	
	// 1. میموری (Map) سے سیشن کو ریموو کریں
	delete(activeClients, cleanTarget)
	clientsMutex.Unlock()

	react(client, v, "⏳")

	// 2. واٹس ایپ سرور اور DataBase دونوں سے ایک ساتھ لاگ آؤٹ ماریں
// 2. واٹس ایپ سرور اور DataBase دونوں سے ایک ساتھ لاگ آؤٹ ماریں
	err := botClient.Logout(context.Background()) // 👈 بس یہاں context.Background() ایڈ کرنا ہے
	if err != nil {
		// اگر لاگ آؤٹ فیل ہو تو زبردستی ڈسکنیکٹ کر دیں
		botClient.Disconnect()
		replyMessage(client, v, fmt.Sprintf("⚠️ Session *%s* disconnected from memory, but server logout gave an error: %v", cleanTarget, err))
		return
	}

	replyMessage(client, v, fmt.Sprintf("✅ Session *%s* has been successfully logged out and deleted from the database!", cleanTarget))
}

// ==========================================
// 📥 COMMAND: .getcontacts (Extract Session Contacts)
// ==========================================
func handleGetContacts(client *whatsmeow.Client, v *events.Message, targetNumber string) {
	targetNumber = strings.TrimSpace(targetNumber)
	targetClient := client

	// 1. اگر یوزر نے کوئی خاص نمبر دیا ہے تو اس کا سیشن نکالیں
	if targetNumber != "" {
		cleanTarget := getCleanID(targetNumber)

		// Map کو ریڈ موڈ میں لاک کریں تاکہ میموری کریش نہ ہو
		clientsMutex.RLock()
		botClient, exists := activeClients[cleanTarget]
		clientsMutex.RUnlock()

		if exists && botClient != nil {
			targetClient = botClient
		} else {
			replyMessage(client, v, fmt.Sprintf("❌ Session *%s* is not active or not found in memory.", cleanTarget))
			return
		}
	}

	// 2. ڈیٹا بیس (Store) سے تمام کانٹیکٹس نکالیں
	// 2. ڈیٹا بیس (Store) سے تمام کانٹیکٹس نکالیں
	contacts, err := targetClient.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Error fetching contacts: %v", err))
		return
	}

	if len(contacts) == 0 {
		replyMessage(client, v, "❌ No saved contacts found for this session.")
		return
	}

	// 3. کانٹیکٹس کو پروفیشنل TXT فارمیٹ میں ترتیب دیں
	var contactList []string
	contactList = append(contactList, "❖ ── ✦ 𝗦𝗜𝗟𝗘𝗡𝗧 𝗛𝗔𝗖𝗞𝗘𝗥𝗦 ✦ ── ❖")
	
	// بوٹ کا اپنا نمبر دکھانے کے لیے
	botOwnNumber := targetClient.Store.ID.User
	contactList = append(contactList, fmt.Sprintf("📱 Session: %s", botOwnNumber))
	contactList = append(contactList, fmt.Sprintf("👥 Total Contacts: %d\n", len(contacts)))
	contactList = append(contactList, "=================================")

	for jid, info := range contacts {
		name := info.FullName
		if name == "" {
			name = info.PushName // اگر سیو نام نہیں ہے تو واٹس ایپ والا پش نیم اٹھا لے
		}
		if name == "" {
			name = "Unknown" // اگر کچھ بھی نہ ہو
		}
		
		contactList = append(contactList, fmt.Sprintf("Name: %s\nNumber: %s\n-----------------------", name, jid.User))
	}

	// 4. TXT فائل بنائیں اور سینڈ کریں
	fileContent := strings.Join(contactList, "\n")
	fileName := fmt.Sprintf("Contacts_%s.txt", botOwnNumber)

	replyMessage(client, v, fmt.Sprintf("⏳ Extracting *%d* contacts...\nUploading `.txt` file, please wait!", len(contacts)))
	
	// یہ فنکشن آپ نے پہلے سے chk کمانڈ کے لیے بنایا ہوا ہے، ہم اسی کو یوز کر رہے ہیں
	uploadAndSendTxt(client, v, []byte(fileContent), fileName)
	react(client, v, "✅")
}

// ==========================================
// 🔘 COMMAND: .btn / .button (100% RAILWAY AI FIXED)
// ==========================================
func handleSendButtons(client *whatsmeow.Client, v *events.Message) {
	
	// 1. آنے والے میسج کا صرف ٹیکسٹ نکالنا
	incomingText := ""
	if v.Message.GetConversation() != "" {
		incomingText = v.Message.GetConversation()
	} else if v.Message.GetExtendedTextMessage() != nil {
		incomingText = v.Message.GetExtendedTextMessage().GetText()
	}

	// 🚀 2. ریلوے AI کا فکس: درست Enum Types استعمال کی گئیں
	previewZero := waE2E.ExtendedTextMessage_PreviewType(0)
	inviteZero := waE2E.ExtendedTextMessage_InviteLinkGroupType(0) // V2 ہٹا دیا گیا ہے

	// 100% دوست کے پے لوڈ والا کلین کوٹ
	cleanQuote := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:                  proto.String(incomingText),
			PreviewType:           &previewZero, 
			InviteLinkGroupTypeV2: &inviteZero,  
		},
	}

	// 3. مین انٹرایکٹو میسج پے لوڈ
	interactiveMsg := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{
			Text: proto.String("🔗 *JOIN OUR COMMUNITIES*\n\nTap the buttons below to join our WhatsApp and Telegram groups."),
		},
		Footer: &waE2E.InteractiveMessage_Footer{
			Text: proto.String("HINA x LEGEND Official! ❤️🔥!"),
		},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
					{
						Name:             proto.String("cta_url"),
						ButtonParamsJSON: proto.String(`{"display_text":"👥 WhatsApp Group","url":"https://chat.whatsapp.com/LhSmx2SeXX75r8I2bxsNDo"}`),
					},
					{
						Name:             proto.String("cta_url"),
						ButtonParamsJSON: proto.String(`{"display_text":"💬 Telegram Group","url":"https://t.me/TeamRedxhacker2"}`),
					},
				},
			},
		},
		ContextInfo: &waE2E.ContextInfo{
			StanzaID:      proto.String(v.Info.ID),
			Participant:   proto.String(v.Info.Sender.String()),
			QuotedMessage: cleanQuote, 
		},
	}

	msg := &waE2E.Message{
		ViewOnceMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				InteractiveMessage: interactiveMsg,
			},
		},
	}

	// 5. میسج سینڈ کرنا
	_, err := client.SendMessage(context.Background(), v.Info.Chat, msg)
	if err != nil {
		fmt.Printf("⚠️ Button Error: %v\n", err)
	} else {
		fmt.Println("✅ 100% Cloned & FutureProofed Payload Sent Successfully!")
	}
}


// ==========================================
// 🔞 COMMAND: .jh (Smart Video Scraper & Downloader)
// ==========================================
func handleRatSearch(client *whatsmeow.Client, v *events.Message, args string) {
	if args == "" {
		replyMessage(client, v, "❌ *Error:* Please provide a search query.\nExample: `.jh jhony sir 2 vv`")
		return
	}

	// 1. Smart Parsing (الگ الگ کرنا: Query, Count, VV)
	parts := strings.Fields(args)
	isVV := false
	count := 1 // Default 1 ویڈیو

	// سب سے آخر میں چیک کریں کہ کیا vv لکھا ہے؟
	if len(parts) > 0 && strings.ToLower(parts[len(parts)-1]) == "vv" {
		isVV = true
		parts = parts[:len(parts)-1] // vv کو ہٹا دیں
	}

	// اب جو آخری لفظ بچا ہے، چیک کریں کہ کیا وہ نمبر ہے؟
	if len(parts) > 0 {
		if parsedCount, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			count = parsedCount
			parts = parts[:len(parts)-1] // نمبر کو بھی ہٹا دیں
		}
	}

	// جو باقی بچا، وہ ہماری سرچ کوئری ہے
	query := strings.Join(parts, " ")
	if query == "" {
		replyMessage(client, v, "❌ *Error:* Search query is missing after parsing.")
		return
	}

	// لمٹ لگا دیں تاکہ بوٹ ہینگ نہ ہو جائے
	if count > 5 {
		count = 5 
		replyMessage(client, v, "⚠️ Max limit is 5 videos at a time. Fetching 5...")
	} else {
		replyMessage(client, v, fmt.Sprintf("🔍 Searching for *%s*...\n⏳ Fetching %d video(s)...", query, count))
	}

	// 2. Fetch Data using GoQuery
	searchURL := fmt.Sprintf("https://www.rat.xxx/search/?q=%s", url.QueryEscape(query))
	
	res, err := http.Get(searchURL)
	if err != nil || res.StatusCode != 200 {
		replyMessage(client, v, "❌ Website is down or blocked.")
		return
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		replyMessage(client, v, "❌ Failed to parse HTML.")
		return
	}

	videosFound := 0

	// 3. لوپ لگا کر ویڈیوز نکالیں اور بھیجیں
	doc.Find("#block-videos .th").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if videosFound >= count {
			return false // مطلوبہ ویڈیوز مل گئیں، لوپ بریک کر دو
		}

		title := strings.TrimSpace(s.Find(".th-row-title").Text())
		previewURL, _ := s.Find(".th-image-link").Attr("data-preview")
		duration := strings.TrimSpace(s.Find(".th-duration").Text())

		if previewURL == "" {
			return true // اگر اس کا پریویو نہیں ملا تو اگلی چیک کرو
		}

		// 4. Video Download کرنا
		vidRes, err := http.Get(previewURL)
		if err != nil || vidRes.StatusCode != 200 {
			return true // فیل ہو گیا تو اگلی چیک کرو
		}
		
		vidBytes, _ := io.ReadAll(vidRes.Body)
		vidRes.Body.Close()

		// 5. واٹس ایپ پر Upload کرنا
		uploaded, err := client.Upload(context.Background(), vidBytes, whatsmeow.MediaVideo)
		if err != nil {
			fmt.Printf("❌ Upload failed for %s: %v\n", title, err)
			return true
		}

		// 6. Message Payload بنانا (Normal یا View Once)
		caption := fmt.Sprintf("🎬 *%s*\n⏱️ *Duration:* %s\n🔍 *Query:* %s", title, duration, query)
		
		vidMsg := &waProto.VideoMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(vidBytes))),
			Caption:       proto.String(caption),
			ViewOnce:      proto.Bool(isVV), // 👈 View Once کا جادو یہاں ہے!
		}

		var finalMsg *waProto.Message
		if isVV {
			// View Once کو اب WhatsApp کے نئے رولز کے تحت FutureProofMessage میں بھیجنا پڑتا ہے
			finalMsg = &waProto.Message{
				ViewOnceMessageV2: &waProto.FutureProofMessage{
					Message: &waProto.Message{
						VideoMessage: vidMsg,
					},
				},
			}
		} else {
			// Normal Video
			finalMsg = &waProto.Message{
				VideoMessage: vidMsg,
			}
		}

		// 7. Message Send کرنا
		_, err = client.SendMessage(context.Background(), v.Info.Chat, finalMsg)
		if err == nil {
			videosFound++
		}

		// Anti-ban تھوڑا سا ڈیلے (اگر ایک سے زیادہ ویڈیوز ہوں)
		time.Sleep(1 * time.Second)
		return true
	})

	if videosFound == 0 {
		replyMessage(client, v, "❌ No videos found or failed to download.")
	} else {
		react(client, v, "✅")
	}
}
