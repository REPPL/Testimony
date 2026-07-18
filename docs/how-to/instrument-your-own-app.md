# Instrument your own app

This guide points the session capture at your own web application instead of the built-in demo. Your app posts events to the capture server; everything downstream — transcribe, merge, report — works unchanged.

## 1. Start the capture server

```sh
testimony demo
```

This creates a fresh session directory (with `manifest.json` anchoring the session clock) and listens on `:8737` (change with `-addr`). A bare `:port` binds loopback (`127.0.0.1`) only, so the capture surface is not published to the network. Ignore the demo page it serves at `/` — you only need its two capture endpoints:

| Endpoint | Body | Appends to |
|---|---|---|
| `POST /api/interactions` | one normalised interaction (a single JSON object) | `interactions.jsonl` (one line per request) |
| `POST /api/events` | a batch of raw rrweb events (a JSON array) | `events.rrweb.jsonl` (one line per array element) |

Both endpoints accept POST only (anything else returns 405), cap the body at 8 MiB, and return `204 No Content` on success. `/api/interactions` rejects invalid JSON with 400; `/api/events` rejects anything that is not a JSON array with 400.

To defend the evidence against cross-origin forgery (CSRF) and DNS-rebinding, the write endpoints require `Content-Type: application/json`, a loopback `Host`, and — when present — a same-origin `Origin`. Post from your app's own origin (see the proxy in step 5) and always set the JSON content type, as the snippet below does. Each accepted body is re-encoded to a single line, so one request is always exactly one JSONL record.

## 2. Add stable `data-testid` attributes

Give every interactive element a stable `data-testid`:

```html
<button data-testid="save-btn">Save</button>
```

Captured selectors then become durable anchors — they survive styling and refactoring, they read well in reports, and they let findings be mapped back to your code by a plain search.

## 3. Post normalised interactions

Each interaction is one JSON object. The fields the pipeline consumes:

| Field | Type | Meaning |
|---|---|---|
| `t` | integer, **required** | event time in epoch milliseconds (`Date.now()`) |
| `kind` | string | event kind, e.g. `"click"` or `"input"` |
| `selector` | string | CSS-like anchor, ideally `[data-testid=...]` |
| `text` | string | short human-readable label of the element |
| `value` | string | new value for input events |
| `route` | string | current route or hash, e.g. `"#general"` |

A minimal capture script, following the same conventions as the demo app — prefer the closest `[data-testid]` ancestor as the selector, keep labels short, and never let capture break the app under test:

```html
<script>
(function () {
  function selectorFor(el) {
    if (!(el instanceof Element)) return "";
    var t = el.closest("[data-testid]");
    if (t) return "[data-testid=" + t.getAttribute("data-testid") + "]";
    if (el.id) return el.tagName.toLowerCase() + "#" + el.id;
    return el.tagName.toLowerCase();
  }
  function post(url, body) {
    try {
      navigator.sendBeacon
        ? navigator.sendBeacon(url, new Blob([JSON.stringify(body)], { type: "application/json" }))
        : fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), keepalive: true });
    } catch (e) { /* capture must never break the app */ }
  }
  function interaction(kind, el, extra) {
    var payload = {
      t: Date.now(),
      kind: kind,
      selector: selectorFor(el),
      text: ((el.closest("[data-testid]") || el).textContent || "").trim().replace(/\s+/g, " ").slice(0, 40),
      route: location.hash || location.pathname
    };
    if (extra) for (var k in extra) payload[k] = extra[k];
    post("/api/interactions", payload);
  }
  document.addEventListener("click", function (e) { interaction("click", e.target); }, true);
  document.addEventListener("change", function (e) {
    var el = e.target;
    var value = el.type === "checkbox" ? String(el.checked) : String(el.value).slice(0, 80);
    interaction("input", el, { value: value });
  }, true);
})();
</script>
```

Unknown extra fields are harmless — the merge step reads only the fields in the table above.

## 4. Optionally post raw rrweb batches

For a full archival record (DOM snapshots, scrolls, mouse movement), also record with [rrweb](https://github.com/rrweb-io/rrweb) and flush the buffer to `/api/events` as a JSON array — every couple of seconds and on `beforeunload`:

```js
var buf = [];
rrweb.record({ emit: function (ev) { buf.push(ev); } });
setInterval(function () {
  if (buf.length) post("/api/events", buf.splice(0, buf.length));
}, 2000);
window.addEventListener("beforeunload", function () {
  if (buf.length) post("/api/events", buf.splice(0, buf.length));
});
```

The rrweb stream is archival only; merge and report consume `interactions.jsonl`.

## 5. Reach the endpoints from your app's origin

If your app runs on its own dev server, the simplest route is to proxy the two `/api` paths to the capture server — most dev servers support this, e.g. in Vite:

```js
// vite.config.js
export default {
  server: {
    proxy: {
      "/api/interactions": "http://localhost:8737",
      "/api/events": "http://localhost:8737",
    },
  },
};
```

The capture script then posts to relative URLs, exactly as in the snippets above.

## 6. Run the session as usual

Record your voice, think aloud, then stop both recorders and run:

```sh
testimony transcribe -session sessions/<dir> -audio <recording.m4a>
testimony merge      -session sessions/<dir>
testimony report     -session sessions/<dir>
```

The report anchors each utterance to your app's `data-testid` selectors. See the [session directory reference](../reference/session-directory.md) for the exact file schemas.
