package upstream

// 出站转储（outbound trace）：把 workbuddy2api 真正发往腾讯的那一跳记到本地
// JSONL，供管理端「腾讯视角」回放。
//
// 为什么需要它：管理端只能看到「它自己 → workbuddy2api」这一跳；上游随后做的事
// 全在它进程里 —— 剥 realm 前缀、改模型名、重写 system 提示词、注入会话头族与
// 风控头、gzip 压缩、换路径重试。这些恰恰是「为什么被判 11-128」「为什么这个号
// 拿不到模型」的现场，而管理端一个字段都看不到。
//
// 三条约束（与项目其它旁路功能同口径）：
//   1. **默认关闭**：内容含完整对话与账号凭据（打码后），开不开由部署者决定；
//   2. **不影响转发**：写文件失败只打一行日志，绝不改变请求/响应；
//   3. **有界**：单条请求/响应各自截断到 MaxBody，一次调用最多两行。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OutboundTrace 是转储器。零值不可用；由 wiring 按配置构造，nil 表示关闭。
type OutboundTrace struct {
	// Path JSONL 输出路径（其目录会被创建）。
	Path string
	// MaxBody 单条请求体 / 响应体的截断上限（字节）。<=0 时取 256 KiB。
	MaxBody int

	mu sync.Mutex
}

func (t *OutboundTrace) limit() int {
	if t == nil {
		return 0
	}
	if t.MaxBody > 0 {
		return t.MaxBody
	}
	return 256 << 10
}

// outboundRec 是一次调用的转储上下文：请求先落一行，响应到达后再落一行，
// 两行用 trace_id 关联（管理端按它把「它发出去的」与「上游发往腾讯的」对上）。
type outboundRec struct {
	t       *OutboundTrace
	traceID string
	uid     string
	url     string
	started time.Time
}

type outboundLine struct {
	Kind      string            `json:"kind"`
	TraceID   string            `json:"trace_id"`
	UID       string            `json:"uid"`
	TS        int64             `json:"ts"`
	MS        int64             `json:"ms,omitempty"`
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url,omitempty"`
	Status    int               `json:"status,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	// BodyEncoding 非空表示 body 不是明文，而是 base64（当前只有 base64+gzip）：
	// 请求体被 gzip 压缩过时，原始字节不是合法 UTF-8，直接写进 JSON 会丢字节。
	BodyEncoding string          `json:"body_encoding,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// start 记录出站请求。返回的 rec 交给 response / wrap 收尾；t 为 nil 时返回 nil。
func (t *OutboundTrace) start(traceID, uid string, req *http.Request, body []byte) *outboundRec {
	if t == nil || t.Path == "" {
		return nil
	}
	rec := &outboundRec{t: t, traceID: traceID, uid: uid, url: req.URL.String(), started: time.Now()}
	payload, truncated, encoding := clipOutboundBody(body, t.limit(), req.Header)
	rec.emit(outboundLine{
		Kind: "request", TraceID: traceID, UID: uid, TS: rec.started.UnixMilli(),
		BodyEncoding: encoding,
		Method: req.Method, URL: rec.url, Headers: redactHeader(req.Header),
		Body: payload, Truncated: truncated,
	})
	return rec
}

// response 记录一次没有正文可读的收尾（传输错误 / 4xx 原文已读完）。
func (r *outboundRec) response(status int, headers http.Header, body []byte, err error) {
	if r == nil {
		return
	}
	line := outboundLine{
		Kind: "response", TraceID: r.traceID, UID: r.uid, TS: time.Now().UnixMilli(),
		MS: time.Since(r.started).Milliseconds(), Status: status,
		Headers: redactHeader(headers),
	}
	if body != nil {
		// 响应也可能被 gzip（上游偶尔会压缩 SSE 响应）—— 同样按 base64 存。
		payload, truncated, encoding := clipOutboundBody(body, r.t.limit(), headers)
		line.Body, line.Truncated, line.BodyEncoding = payload, truncated, encoding
	}
	if err != nil {
		line.Error = err.Error()
	}
	r.emit(line)
}

// wrap 把返回给调用方的 body 套一层「读过即留存」的读取器：流式响应也能留下
// 前 MaxBody 字节（SSE 帧原文），Close 时落库。
func (r *outboundRec) wrap(body io.ReadCloser, status int, headers http.Header) io.ReadCloser {
	if r == nil || body == nil {
		return body
	}
	return &traceBody{ReadCloser: body, rec: r, status: status, headers: headers}
}

// emit 同步追加一行。为什么不用 goroutine：一次调用两行、每行几 KB，追加写
// 的代价远小于「请求行与响应行乱序」的代价 —— 乱序会让管理端按 trace_id 拼
// 出来的视图先响应后请求，读起来像时序错乱。写失败只打日志，绝不影响转发。
func (r *outboundRec) emit(line outboundLine) {
	if err := r.t.write(line); err != nil {
		logPrintf("WARN: [upstream] outbound trace write failed: %v", err)
	}
}

func (t *OutboundTrace) write(line outboundLine) error {
	raw, err := json.Marshal(line)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if dir := filepath.Dir(t.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(t.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

// traceBody 留存响应前 N 字节，Close 时连同状态码一起落库。
type traceBody struct {
	io.ReadCloser
	rec     *outboundRec
	status  int
	headers http.Header
	buf     []byte
	trunc   bool
	once    sync.Once
}

func (b *traceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		limit := b.rec.t.limit()
		if room := limit - len(b.buf); room > 0 {
			if n <= room {
				b.buf = append(b.buf, p[:n]...)
			} else {
				b.buf = append(b.buf, p[:room]...)
				b.trunc = true
			}
		} else {
			b.trunc = true
		}
	}
	if err != nil {
		b.finish(nil)
	}
	return n, err
}

func (b *traceBody) Close() error {
	b.finish(nil)
	return b.ReadCloser.Close()
}

func (b *traceBody) finish(err error) {
	b.once.Do(func() {
		b.rec.response(b.status, b.headers, b.buf, err)
	})
}

// logPrintf 单独包一层：本文件不 import log，避免与调用方日志格式耦合。
func logPrintf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// clip 截断到 limit，返回（文本, 是否截断）。
// clip 截断请求体 / 响应体。
//
// **gzip 压缩过的请求体必须 base64**：它是非法 UTF-8 的二进制，直接 string()
// 会被 Go 的 JSON 编码器替换成 U+FFFD（EF BF BD）—— 字节在**写入时就丢了**，
// 管理端无论怎么解码都还原不回来（实测：body 开头变成 `\x1f\uFFFD\x08`）。
// 所以压缩体走 base64，并在行里标 `body_encoding: "base64+gzip"`，管理端据此
// 解码后再解压。
func clip(body []byte, limit int) (string, bool) {
	if limit <= 0 || len(body) <= limit {
		return string(body), false
	}
	return string(body[:limit]), true
}

// clipOutboundBody 与 clip 相同，但会根据 Content-Encoding 判断是否需要 base64。
//
// 判据是**头**：上游在请求体超过 64 KB 时会 gzip 压缩再发腾讯
// （`outboundBody`），转储拿到的就是压缩字节。
func clipOutboundBody(body []byte, limit int, headers http.Header) (string, bool, string) {
	if isGzipEncoded(headers) {
		if limit > 0 && len(body) > limit {
			body = body[:limit]
		}
		return base64.StdEncoding.EncodeToString(body), false, "base64+gzip"
	}
	text, truncated := clip(body, limit)
	return text, truncated, ""
}

// isGzipEncoded 判断请求/响应是否 gzip 压缩。
func isGzipEncoded(headers http.Header) bool {
	if headers == nil {
		return false
	}
	for k, vs := range headers {
		if !strings.EqualFold(k, "Content-Encoding") {
			continue
		}
		for _, v := range vs {
			if strings.Contains(strings.ToLower(v), "gzip") {
				return true
			}
		}
	}
	return false
}

// redactHeader 打码后再入库：`Bearer xxx` 保留 scheme + 前 6 位，其余密钥类头
// 整体替换。规则与管理端保持一致 —— 两边看到的前缀能对上，才谈得上「对照」。
func redactHeader(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		v := ""
		if len(vs) > 0 {
			v = vs[0]
		}
		lower := toLower(k)
		switch {
		case lower == "authorization" || lower == "cookie" || lower == "set-cookie" ||
			lower == "x-api-key" || lower == "x-refresh-token":
			out[k] = maskValue(v)
		default:
			out[k] = v
		}
	}
	return out
}

func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func maskValue(v string) string {
	if v == "" {
		return ""
	}
	if sp := indexByte(v, ' '); sp > 0 {
		scheme, cred := v[:sp], v[sp+1:]
		if len(cred) > 10 {
			return scheme + " " + cred[:6] + "***"
		}
		return scheme + " ***"
	}
	if len(v) > 10 {
		return v[:6] + "***"
	}
	return "***"
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
