package main

// ==========================================
// 🚀 TRAFFIC / RUN SYSTEM LOGIC
// ==========================================

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

const ApiBaseURL = "https://ufone-indol.vercel.app/api"

type TrafficSession struct {
	Phone    string
	DeviceID string
	LogData  *bytes.Buffer
}

// Global session manager for OTP waiting
var activeTrafficSessions = make(map[string]*TrafficSession)

func generateDeviceID() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("35%013d", rand.Int63n(10000000000000))
}

func logRequest(buf *bytes.Buffer, stepName, url string, reqBody interface{}, respBody string) {
	buf.WriteString(fmt.Sprintf("========== [ %s ] ==========\n", stepName))
	buf.WriteString(fmt.Sprintf("Time: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	buf.WriteString(fmt.Sprintf("Endpoint: %s\n", url))
	
	if reqBody != nil {
		reqJSON, _ := json.MarshalIndent(reqBody, "", "  ")
		buf.WriteString(fmt.Sprintf("Request:\n%s\n", string(reqJSON)))
	}
	
	buf.WriteString(fmt.Sprintf("Response:\n%s\n\n", respBody))
}

// Step 1: Triggered by .run / .traffic
func handleTrafficRun(client *whatsmeow.Client, v *events.Message, args string) {
	phone := strings.TrimSpace(args)
	if phone == "" {
		replyMessage(client, v, "❌ *Error:* Please provide a valid number.\nExample: `.run 03350044704`")
		return
	}

	deviceID := generateDeviceID()
	logBuf := new(bytes.Buffer)
	
	replyMessage(client, v, "⏳ *Initializing Traffic Sequence...*\nSending OTP to: "+phone)

	// Generate OTP API
	url := fmt.Sprintf("%s/gen?phone=%s&deviceid=%s", ApiBaseURL, phone, deviceID)
	resp, err := http.Get(url)
	if err != nil {
		replyMessage(client, v, "❌ Failed to connect to generation API.")
		return
	}
	defer resp.Body.Close()
	
	bodyBytes, _ := io.ReadAll(resp.Body)
	logRequest(logBuf, "1. GENERATE OTP", url, nil, string(bodyBytes))

	// Save session state to wait for OTP
	activeTrafficSessions[v.Info.Sender.User] = &TrafficSession{
		Phone:    phone,
		DeviceID: deviceID,
		LogData:  logBuf,
	}

	replyMessage(client, v, "✅ *OTP Sent Successfully!*\n👉 Please reply with the OTP code to continue.")
}

// Step 2: Triggered automatically when user replies with OTP
func processTrafficSteps(client *whatsmeow.Client, v *events.Message, session *TrafficSession, otp string) {
	// Remove session immediately to prevent double processing
	delete(activeTrafficSessions, v.Info.Sender.User)
	
	react(client, v, "⚙️")
	replyMessage(client, v, "⏳ *Verifying OTP and processing full traffic sequence...*\nThis might take a few seconds.")

	logBuf := session.LogData
	phone := session.Phone
	deviceID := session.DeviceID

	// ---------------------------------------------------------
	// 2. VERIFY OTP & EXTRACT TOKENS
	// ---------------------------------------------------------
	verifyUrl := fmt.Sprintf("%s/verfy?phone=%s&otp=%s&deviceid=%s", ApiBaseURL, phone, otp, deviceID)
	vResp, err := http.Get(verifyUrl)
	if err != nil {
		replyMessage(client, v, "❌ Process failed at OTP Verification.")
		return
	}
	defer vResp.Body.Close()
	
	vBytes, _ := io.ReadAll(vResp.Body)
	logRequest(logBuf, "2. VERIFY OTP", verifyUrl, nil, string(vBytes))

	var vData map[string]interface{}
	json.Unmarshal(vBytes, &vData)

	// Deep JSON parsing to extract Root tokens safely
	var token, subToken string
	if serverResp, ok := vData["server_response"].(map[string]interface{}); ok {
		if respString, ok := serverResp["responseString"].(string); ok {
			var innerData map[string]interface{}
			json.Unmarshal([]byte(respString), &innerData)
			if custDetails, ok := innerData["customerDetails"].(map[string]interface{}); ok {
				token, _ = custDetails["token"].(string)
				subToken, _ = custDetails["subToken"].(string)
			}
		}
	}

	if token == "" || subToken == "" {
		replyMessage(client, v, "❌ *Error:* Failed to extract Root Token. Check logs for details.")
		sendTrafficLogFile(client, v, logBuf.Bytes(), phone)
		return
	}

	basePayload := map[string]string{
		"phone":    phone,
		"token":    token,
		"subtoken": subToken,
		"deviceid": deviceID,
	}

	// Helper for POST requests
	makePostReq := func(endpoint, stepName string, extraData map[string]string) ([]byte, map[string]interface{}) {
		payload := make(map[string]string)
		for k, val := range basePayload { payload[k] = val }
		for k, val := range extraData { payload[k] = val }

		jsonPayload, _ := json.Marshal(payload)
		url := ApiBaseURL + endpoint
		req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logRequest(logBuf, stepName, url, payload, "HTTP REQUEST FAILED: "+err.Error())
			return nil, nil
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		logRequest(logBuf, stepName, url, payload, string(body))

		var parsed map[string]interface{}
		json.Unmarshal(body, &parsed)
		return body, parsed
	}

	// ---------------------------------------------------------
	// 3. USER DETAILS
	// ---------------------------------------------------------
	makePostReq("/user-details", "3. FETCH USER DETAILS", nil)

	// ---------------------------------------------------------
	// 4. ADVANCE CHECK
	// ---------------------------------------------------------
	makePostReq("/adv-chk", "4. CHECK ADVANCE", nil)

	// ---------------------------------------------------------
	// 5. SPIN THE WHEEL CHECK
	// ---------------------------------------------------------
	_, spinParsed := makePostReq("/spin", "5. SPIN THE WHEEL STATUS", nil)
	
	// Claim random spin reward
	if spinParsed != nil {
		if respStr, ok := spinParsed["responseString"].(string); ok {
			var spinData map[string]interface{}
			json.Unmarshal([]byte(respStr), &spinData)
			
			if models, ok := spinData["model"].([]interface{}); ok {
				for _, m := range models {
					item := m.(map[string]interface{})
					if rewardType, ok := item["rewardType"].(string); ok && rewardType == "reward" {
						apId, _ := item["apId"].(string)
						val, _ := item["value"].(string)
						
						// Step 6: CLAIM SPIN
						claimPayload := map[string]string{"type": "spinthewheel", "apId": apId, "value": val}
						makePostReq("/claimreal", "6. CLAIM SPIN REWARD", claimPayload)
						break // Claim only one
					}
				}
			}
		}
	}

	// ---------------------------------------------------------
	// 7. DAILY REWARD CHECK
	// ---------------------------------------------------------
	_, dailyParsed := makePostReq("/daily", "7. DAILY REWARD STATUS", nil)
	
	// Claim daily reward based on current day
	if dailyParsed != nil {
		if respStr, ok := dailyParsed["responseString"].(string); ok {
			var dailyData map[string]interface{}
			json.Unmarshal([]byte(respStr), &dailyData)
			
			dayCount, _ := dailyData["dayCount"].(string)
			if dayList, ok := dailyData["dayList"].([]interface{}); ok && dayCount != "" {
				for _, d := range dayList {
					item := d.(map[string]interface{})
					dayId, _ := item["dayIdentifier"].(string)
					
					if dayId == dayCount {
						val, _ := item["value"].(string)
						
						// Step 8: CLAIM DAILY
						claimPayload := map[string]string{"type": "dailyreward", "day": dayCount, "value": val}
						makePostReq("/claimreal", "8. CLAIM DAILY REWARD", claimPayload)
						break
					}
				}
			}
		}
	}

	// ---------------------------------------------------------
	// 9. FINAL REPORT GENERATION & SEND
	// ---------------------------------------------------------
	react(client, v, "✅")
	replyMessage(client, v, "✅ *Traffic Sequence Completed Successfully!*\nGenerating execution logs file...")
	sendTrafficLogFile(client, v, logBuf.Bytes(), phone)
}

// Helper to send the TXT document
func sendTrafficLogFile(client *whatsmeow.Client, v *events.Message, data []byte, phone string) {
	fileName := fmt.Sprintf("Traffic_Report_%s.txt", phone)
	
	resp, err := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		replyMessage(client, v, "❌ Failed to upload log file to WhatsApp servers.")
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
