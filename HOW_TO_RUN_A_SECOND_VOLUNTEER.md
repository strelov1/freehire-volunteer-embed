# Running the volunteer client on a second machine

## Step 1 — install Go (if you don't have it)

```bash
brew install go   # on Mac
# or see https://go.dev/doc/install for Linux/Windows
```

## Step 2 — clone and build the client

```bash
git clone https://github.com/strelov1/freehire-volunteer-embed.git
cd freehire-volunteer-embed
go build -o volunteer-client ./cmd/volunteer-client
```

## Step 3 — run a local embedding server

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
import torch
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from transformers import AutoTokenizer, AutoModel

MODEL_NAME = "intfloat/multilingual-e5-base"
DEVICE = "mps" if torch.backends.mps.is_available() else "cpu"

print(f"loading {MODEL_NAME} on {DEVICE}...")
tokenizer = AutoTokenizer.from_pretrained(MODEL_NAME)
model = AutoModel.from_pretrained(MODEL_NAME).to(DEVICE).eval()
print("ready")


def embed(texts):
    batch = tokenizer(texts, padding=True, truncation=True, max_length=512, return_tensors="pt").to(DEVICE)
    with torch.no_grad():
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

## Step 4 — run the client

For the API key — **reuse the same one already in use** (ask me, or find it where I already showed it once in our conversation) — the same key on two machines at once is fine by design for this test.

```bash
cd freehire-volunteer-embed
SERVER_URL="https://freehire.me" \
API_KEY="<your fhk_ key>" \
LOCAL_TEI_URL="http://127.0.0.1:8080/embed" \
BATCH_SIZE=5 \
POLL_INTERVAL=10s \
./volunteer-client
```

## How to confirm it's actually working

The log should show lines like:

```
submit job 123456: accepted
leased=5 submitted=5
```

Seeing this means it's working — real jobs are actually being processed.

## Stopping it

`Ctrl+C` in the terminal where the client is running.
