# Running the volunteer client

## Step 1 — download the client

No Go install, no building — grab a prebuilt binary from the
[latest release](https://github.com/strelov1/freehire-volunteer-embed/releases/latest)
for your OS:

- macOS (Apple Silicon): `volunteer-client-darwin-arm64`
- macOS (Intel): `volunteer-client-darwin-amd64`
- Linux (x86_64): `volunteer-client-linux-amd64`
- Linux (ARM64): `volunteer-client-linux-arm64`
- Windows: `volunteer-client-windows-amd64.exe`

Make it executable (macOS/Linux):

```bash
chmod +x volunteer-client-*
```

(If you'd rather build from source instead:
`git clone https://github.com/strelov1/freehire-volunteer-embed.git && cd freehire-volunteer-embed && go build -o volunteer-client ./cmd/volunteer-client`)

## Step 2 — run a local embedding server

Needs Python 3 + venv:

```bash
mkdir -p ~/volunteer-embed-local-tei
cd ~/volunteer-embed-local-tei
python3 -m venv .venv
source .venv/bin/activate
pip install torch transformers numpy
```

Save this as `server.py` in the same folder:

```python
#!/usr/bin/env python3
import json
import threading
import torch
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from transformers import AutoTokenizer, AutoModel

MODEL_NAME = "intfloat/multilingual-e5-base"
DEVICE = "mps" if torch.backends.mps.is_available() else "cpu"

print(f"loading {MODEL_NAME} on {DEVICE}...")
tokenizer = AutoTokenizer.from_pretrained(MODEL_NAME)
model = AutoModel.from_pretrained(MODEL_NAME).to(DEVICE).eval()
print("ready")

# The GPU backend is not thread-safe: two requests running the model at
# once crash the server. Requests are still read and written in parallel.
_model_lock = threading.Lock()


def embed(texts):
    with _model_lock, torch.no_grad():
        batch = tokenizer(texts, padding=True, truncation=True, max_length=512, return_tensors="pt").to(DEVICE)
        out = model(**batch)
        mask = batch["attention_mask"].unsqueeze(-1).float()
        summed = (out.last_hidden_state * mask).sum(dim=1)
        counts = mask.sum(dim=1).clamp(min=1e-9)
        pooled = summed / counts
        normalized = torch.nn.functional.normalize(pooled, p=2, dim=1)
        return normalized.cpu().tolist()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_POST(self):
        if self.path != "/embed":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length))
        vectors = embed(body["inputs"])
        payload = json.dumps(vectors).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


if __name__ == "__main__":
    addr = ("127.0.0.1", 8080)
    print(f"listening on http://{addr[0]}:{addr[1]}/embed")
    ThreadingHTTPServer(addr, Handler).serve_forever()
```

Run it (leave it running in its own terminal window):

```bash
python3 server.py
```

Wait for the `ready` line (first run downloads the model, takes a minute or two).

Note: if the machine has no Apple MPS (not a Mac), it'll run on CPU instead — slower, but still works.

## Step 3 — run the client

For the API key — **reuse the same one already in use** (ask me, or find it where I already showed it once in our conversation) — the same key on two machines at once is fine by design for this test.

```bash
SERVER_URL="https://freehire.me" \
API_KEY="<your fhk_ key>" \
LOCAL_TEI_URL="http://127.0.0.1:8080/embed" \
BATCH_SIZE=50 \
POLL_INTERVAL=10s \
LOAD=medium \
./volunteer-client-darwin-arm64   # use the binary name matching your OS
```

`LOAD` is how much of your machine the client may use — the share of time
`server.py` is kept busy:

- `easy` — 25%, barely noticeable
- `medium` — 50%, the default
- `hard` — 100%, the embedder never waits
- or any percentage from 1 to 100, e.g. `LOAD=70`

Run **one** client per machine: it already keeps several jobs in flight at
once, so a second client only competes with the first for the same
`server.py`. `POLL_INTERVAL` is how long it waits when the server has no
work for it (or a request failed) — never after a batch that went through.

`BATCH_SIZE` is how many jobs the client pulls from the server per round trip
— raising it reduces network overhead. By default the client does **not**
hand that whole batch to `server.py` in one call: it starts by sending just 5
texts at a time, and automatically grows that number (up to 16 —
`LOCAL_EMBED_CHUNK_SIZE_MAX`; on a Mac a bigger call is no faster and costs
memory) for as long as the local embedder keeps responding quickly and the
machine has memory to spare, backing off the moment it slows down or
errors. This runs by itself — no tuning required, and it keeps
re-adjusting for as long as the client keeps running.

If you want to pin it to a fixed size instead (e.g. to deliberately keep an
older/shared machine throttled), set `LOCAL_EMBED_CHUNK_SIZE` and the
auto-tuning is skipped entirely:

```bash
LOCAL_EMBED_CHUNK_SIZE=5 \
```

## How to confirm it's actually working

The log should show lines like:

```
submit job 123456: accepted
worker 1: leased=50 submitted=50
```

Seeing this means it's working — real jobs are actually being processed.

## Stopping it

`Ctrl+C` in the terminal where the client is running.
