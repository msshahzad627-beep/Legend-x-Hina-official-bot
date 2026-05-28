import sys
import json
import urllib.parse
import requests
import subprocess
import os
import random
import time
from playwright.sync_api import sync_playwright

PROXY_LIST = [
    "31.59.20.176:6754:wwwsyxzg:582ygxexguhx",
    "23.95.150.145:6114:wwwsyxzg:582ygxexguhx",
    "198.23.239.134:6540:wwwsyxzg:582ygxexguhx",
    "45.38.107.97:6014:wwwsyxzg:582ygxexguhx",
    "107.172.163.27:6543:wwwsyxzg:582ygxexguhx",
    "198.105.121.200:6462:wwwsyxzg:582ygxexguhx",
    "216.10.27.159:6837:wwwsyxzg:582ygxexguhx",
    "142.111.67.146:5611:wwwsyxzg:582ygxexguhx",
    "191.96.254.138:6185:wwwsyxzg:582ygxexguhx",
    "31.58.9.4:6077:wwwsyxzg:582ygxexguhx"
]

SESSION_DIR = "sessions"
if not os.path.exists(SESSION_DIR):
    os.makedirs(SESSION_DIR, exist_ok=True)

def atomic_write_json(filepath, data):
    """فائل کو کرپٹ ہونے سے بچانے کے لیے محفوظ طریقے سے رائٹ کرنا"""
    tmp_path = f"{filepath}.{random.randint(10000, 99999)}.tmp"
    try:
        with open(tmp_path, "w") as f:
            json.dump(data, f)
        os.replace(tmp_path, filepath) # یہ کمانڈ ونڈوز اور لینکس دونوں پر Atomic ہے
    except Exception as e:
        if os.path.exists(tmp_path):
            os.remove(tmp_path)

def get_audio_duration(file_path):
    try:
        result = subprocess.run(
            ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", file_path],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=10
        )
        return float(result.stdout.strip())
    except:
        return 3.0

def get_proxy_settings(proxy_str):
    ip, port, user, pw = proxy_str.split(":")
    pw_proxy = {"server": f"http://{ip}:{port}", "username": user, "password": pw}
    req_proxy = {"http": f"http://{user}:{pw}@{ip}:{port}", "https": f"http://{user}:{pw}@{ip}:{port}"}
    return pw_proxy, req_proxy, ip.replace(".", "_")

def load_session_meta(meta_file):
    if os.path.exists(meta_file):
        try:
            with open(meta_file, "r") as f:
                return json.load(f)
        except:
            pass
    return {"count": 0}

def clear_session(state_file, meta_file):
    if os.path.exists(state_file): os.remove(state_file)
    if os.path.exists(meta_file): os.remove(meta_file)

def convert_voice(input_file, voice_id="7", pitch=16, max_retries=5):
    if not os.path.exists(input_file):
        print("[ERROR] Input file not found.")
        sys.exit(1)

    duration = get_audio_duration(input_file)
    
    # آٹو پراکسی سوئچ اور ری ٹرائی لوپ
    for attempt in range(1, max_retries + 1):
        proxy_str = random.choice(PROXY_LIST)
        pw_proxy, req_proxy, proxy_id = get_proxy_settings(proxy_str)
        
        state_file = os.path.join(SESSION_DIR, f"state_{proxy_id}.json")
        meta_file = os.path.join(SESSION_DIR, f"meta_{proxy_id}.json")
        
        meta = load_session_meta(meta_file)
        
        if meta["count"] >= 5:
            clear_session(state_file, meta_file)
            meta["count"] = 0
            
        print(f"\n[ATTEMPT {attempt}/{max_retries}] Using Proxy: {proxy_str.split(':')[0]} | Use Count: {meta['count'] + 1}/5")
        
        try:
            with sync_playwright() as p:
                browser = p.chromium.launch(headless=True, proxy=pw_proxy)
                context_args = {
                    "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
                    "viewport": {"width": 1280, "height": 720}
                }
                
                if os.path.exists(state_file):
                    context_args["storage_state"] = state_file
                    
                context = browser.new_context(**context_args)
                page = context.new_page()
                page.set_default_timeout(45000) # 45 سیکنڈ کا ہارڈ ٹائم آؤٹ
                
                print("[STEP 1] Fetching XSRF-TOKEN...")
                page.goto("https://voice.ai/tools/voice-changer", wait_until="networkidle")
                
                xsrf_token = next((urllib.parse.unquote(c['value']) for c in context.cookies() if c['name'] == 'XSRF-TOKEN'), None)
                if not xsrf_token:
                    raise Exception("Cloudflare Blocked or Token not found")

                print("[STEP 2] Requesting Google Cloud Upload URL...")
                upload_req = context.request.post(
                    "https://voice.ai/api/upload/get-google-url",
                    headers={"x-xsrf-token": xsrf_token, "accept": "application/json"},
                    data={"file_type": "audio/mpeg", "filename": None},
                    timeout=30000
                )
                if upload_req.status != 200:
                    raise Exception(f"Upload URL failed: {upload_req.status}")

                upload_data = upload_req.json()
                
                print("[STEP 3] Uploading audio...")
                with open(input_file, "rb") as f:
                    audio_bytes = f.read()
                
                put_resp = requests.put(upload_data["url"], data=audio_bytes, headers={"Content-Type": "audio/mpeg"}, proxies=req_proxy, timeout=30)
                if put_resp.status_code != 200:
                    raise Exception(f"Google Cloud upload failed: {put_resp.status_code}")
                
                print("[STEP 4] Sending voice conversion request...")
                process_req = context.request.post(
                    "https://voice.ai/api/web-tools/queue/store/voice-changer",
                    headers={"x-xsrf-token": xsrf_token, "accept": "application/json"},
                    data={
                        "path": f"tmp/{upload_data['fileKey']}",
                        "original_filename": "wa_audio.mp3",
                        "voice_id": str(voice_id),
                        "pitch": int(pitch),
                        "duration": float(duration)
                    },
                    timeout=60000
                )
                
                result = json.loads(process_req.text())
                if "data" in result and "url" in result["data"]:
                    final_url = result["data"]["url"]
                    print(f"\n[COMPLETE] Result Found: {final_url}")
                    print(f"RESULT_URL:{final_url}") # آپ کا Go بوٹ اس لائن کو پکڑے گا
                    
                    # کامیابی پر میٹا اور سیشن اپڈیٹ کریں
                    meta["count"] += 1
                    atomic_write_json(meta_file, meta)
                    
                    # Playwright کا سیشن بھی محفوظ طریقے سے سیو کرنا
                    tmp_state = state_file + ".tmp"
                    context.storage_state(path=tmp_state)
                    os.replace(tmp_state, state_file)
                    
                    context.close()
                    browser.close()
                    return # سب کچھ ہو گیا، سکرپٹ کامیابی سے ختم کرو
                else:
                    raise Exception(f"Conversion Server Error: {result.get('message', 'Unknown error')}")

        except Exception as e:
            print(f"[FAILED] Attempt {attempt} failed: {str(e)}")
            clear_session(state_file, meta_file) # اگر ایرر آئے تو اس پراکسی کا سیشن کلئیر کر دو تاکہ اگلی بار نیا بنے
            time.sleep(1) # اگلی پراکسی ٹرائی کرنے سے پہلے 1 سیکنڈ کا وقفہ
            continue # لوپ کو جاری رکھو اور اگلی پراکسی ٹرائی کرو

    # اگر لوپ ختم ہو جائے اور کوئی بھی پراکسی کام نہ کرے
    print("[CRITICAL ERROR] All proxies and retries failed.")
    sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("[ERROR] No input file provided.")
        sys.exit(1)
    
    convert_voice(sys.argv[1])
