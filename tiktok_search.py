import sys
import json
import time
from playwright.sync_api import sync_playwright

def print_debug(msg):
    sys.stderr.write(f"🐍 [DEBUG] {msg}\n")
    sys.stderr.flush()

def search_tiktok(query, limit=10):
    results = []
    print_debug(f"🚀 Starting Network Sniffer for: {query}")

    with sync_playwright() as p:
        try:
            # 1. Browser Setup (With Network Monitoring)
            browser = p.chromium.launch(
                headless=True,
                args=[
                    "--no-sandbox",
                    "--disable-gpu",
                    "--disable-blink-features=AutomationControlled"
                ]
            )
            
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
                viewport={"width": 1920, "height": 1080}
            )
            
            page = context.new_page()

            # 2. 🔥 NETWORK INTERCEPTOR (The Magic Part)
            def handle_response(response):
                try:
                    if "item_list" in response.url or "search_item" in response.url or "video" in response.url:
                        pass
                except:
                    pass

            page.on("response", handle_response)

            # 3. Navigate
            if query.startswith("#"):
                url = f"https://www.tiktok.com/tag/{query[1:]}"
            else:
                url = f"https://www.tiktok.com/search?q={query}"

            print_debug(f"Navigating to: {url}")
            page.goto(url, timeout=60000, wait_until="domcontentloaded")

            # 4. Scroll to trigger XHR requests
            print_debug("Scrolling to fetch data...")
            for _ in range(5):
                page.keyboard.press("End")
                time.sleep(2)

            # 🚨 NEW: PRINT FULL HTML PAGE (ڈیبگنگ کے لیے پورا پیج کنسول پر پھینکیں)
            print_debug("================ PAGE HTML START ================")
            html_content = page.content()
            try:
                # Unicode ایررز سے بچنے کے لیے سیدھا stderr پر لکھ رہے ہیں
                sys.stderr.write(html_content + "\n")
            except UnicodeEncodeError:
                # اگر کوئی ایموجی وغیرہ کا مسئلہ ہو تو اسے ہینڈل کر لے گا
                sys.stderr.write(html_content.encode('utf-8', 'ignore').decode('utf-8') + "\n")
            sys.stderr.flush()
            print_debug("================ PAGE HTML END ================")

            # 5. 💪 BRUTE FORCE EXTRACTION
            print_debug("Extracting video objects...")
            
            data = page.evaluate("""
                () => {
                    const items = [];
                    const candidates = document.querySelectorAll('div[data-e2e="search_top_item"], div[data-e2e="search_item"], a');
                    
                    candidates.forEach(el => {
                        let link = el.getAttribute('href');
                        if (!link && el.tagName === 'DIV') {
                            const a = el.querySelector('a');
                            if (a) link = a.getAttribute('href');
                        }

                        if (link && link.includes('/video/')) {
                            let title = el.innerText || "";
                            const img = el.querySelector('img');
                            if (img && img.alt && img.alt.length > title.length) title = img.alt;
                            
                            if (link.startsWith('/')) link = "https://www.tiktok.com" + link;

                            title = title.replace(/\\n/g, ' ').trim();
                            if (title.length > 80) title = title.substring(0, 77) + "...";
                            if (!title) title = "TikTok Video";

                            if (!items.find(i => i.url === link)) {
                                items.push({ title: title, url: link });
                            }
                        }
                    });
                    return items;
                }
            """)
            
            # فلٹر کریں
            filtered_results = [item for item in data if "/video/" in item['url']]
            
            print_debug(f"Found {len(filtered_results)} valid videos.")
            results = filtered_results[:limit]

        except Exception as e:
            print_debug(f"🔥 Error: {str(e)}")
        finally:
            if 'browser' in locals():
                browser.close()

    # Final JSON Output (یہ Go کو ملے گا)
    print(json.dumps(results))

if __name__ == "__main__":
    query = "funny"
    if len(sys.argv) > 1:
        query = sys.argv[1]
    search_tiktok(query)