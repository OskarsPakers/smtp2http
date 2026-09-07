import http.server, json, sys, threading, smtplib, os, glob, time

CORPUS = sys.argv[1]; OUT = sys.argv[2]
received = {}
current = {"name": None}

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers['Content-Length']))
        received[current["name"]] = json.loads(body)
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass

srv = http.server.HTTPServer(('0.0.0.0', 8099), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()

for path in sorted(glob.glob(os.path.join(CORPUS, '*.eml'))):
    name = os.path.basename(path)[:-4]
    current["name"] = name
    raw = open(path, 'rb').read()
    try:
        s = smtplib.SMTP('127.0.0.1', 2525, timeout=10)
        s.sendmail('alice@sender.invalid', ['bob@example.com'], raw)
        s.quit()
    except Exception as e:
        print(f"  {name}: SMTP ERROR {e}"); continue
    time.sleep(0.35)
    if name in received:
        json.dump(received[name], open(os.path.join(OUT, name + '.json'), 'w'),
                  indent=2, sort_keys=True, ensure_ascii=False)
        print(f"  {name}: captured")
    else:
        print(f"  {name}: NO WEBHOOK RECEIVED")
