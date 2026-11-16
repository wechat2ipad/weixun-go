package weixun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// UploadService 封装文件上传能力。
//
// 协议端实测只有 **一个** multipart 接口：POST /message/CdnBigFileUpload
//   - file    必填，≤200MB
//   - thumb   可选，≤10MB
//   - fileType 2图片 / 4视频 / 5文件 / 6小视频 / 7大文件 / 9-1G / 11语音
//   - toUser  接收方（上传后直接发送）
//   - aesKeyHex 可选
//
// 其余"上传"类接口全部是 JSON + base64（SendImageMessage / UploadImageToCDN /
// SendVoice / UploadHeadImage / UploadAppAttach 等），用 Base64* 系列方法。
type UploadService struct {
	c *Client
}

// 协议端 fileType 取值。
const (
	FileTypeImage = 2
	FileTypeVideo = 4
	FileTypeFile  = 5
	FileTypeMini  = 6
	FileTypeBig   = 7
	FileTypeOneG  = 9
	FileTypeVoice = 11
)

// BigFileUploadOptions 是大文件上传参数。
type BigFileUploadOptions struct {
	// FilePath 本地文件路径
	FilePath string
	// ThumbPath 缩略图路径，可选
	ThumbPath string
	// FileType 见 FileType* 常量，视频传 4
	FileType int
	// ToUser 接收方；留空则只上传不发送
	ToUser string
	// AESKeyHex 可选
	AESKeyHex string
}

// UploadService 入口（懒初始化，避免 Client 结构改动）。
func (c *Client) Upload() *UploadService { return &UploadService{c} }

// BigFile 上传大文件（multipart/form-data）。
//
// 这是协议端唯一的 multipart 接口，不能走 JSON body。
func (u *UploadService) BigFile(ctx context.Context, opts *BigFileUploadOptions) (json.RawMessage, error) {
	if opts == nil || opts.FilePath == "" {
		return nil, fmt.Errorf("weixun: 必须指定 FilePath")
	}
	fi, err := os.Stat(opts.FilePath)
	if err != nil {
		return nil, fmt.Errorf("weixun: 读取文件失败: %w", err)
	}
	if fi.Size() > 200*1024*1024 {
		return nil, fmt.Errorf("weixun: 文件超过 200MB 上限")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// file
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	part, err := w.CreateFormFile("file", filepath.Base(opts.FilePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}

	// thumb（可选）
	if opts.ThumbPath != "" {
		tf, err := os.Open(opts.ThumbPath)
		if err == nil {
			defer tf.Close()
			if tp, err := w.CreateFormFile("thumb", filepath.Base(opts.ThumbPath)); err == nil {
				io.Copy(tp, tf)
			}
		}
	}

	ft := opts.FileType
	if ft == 0 {
		ft = FileTypeFile
	}
	w.WriteField("fileType", fmt.Sprintf("%d", ft))
	if opts.ToUser != "" {
		w.WriteField("toUser", opts.ToUser)
	}
	if opts.AESKeyHex != "" {
		w.WriteField("aesKeyHex", opts.AESKeyHex)
	}
	w.Close()

	u2, err := url.Parse(u.c.baseURL + "/message/CdnBigFileUpload")
	if err != nil {
		return nil, err
	}
	q := u2.Query()
	q.Set("key", u.c.key)
	u2.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "POST", u2.String(), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", "weixun-go/0.1.0")

	resp, err := u.c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weixun: 上传失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{Code: CodeFailGeneric, Text: "上传响应非法", StatusCode: resp.StatusCode, Body: raw}
	}
	if !env.OK() {
		return nil, newAPIError(&env, resp.StatusCode, raw)
	}
	return env.Data, nil
}

// Base64FromFile 读取本地文件并编码成 base64，供 SendImage 等接口使用。
func (u *UploadService) Base64FromFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// Base64FromReader 从流读取并编码。
func (u *UploadService) Base64FromReader(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ImageDataURI 生成协议端可直接接受的 data URI 形式。
// 协议端会用 strings.Split(",") 取最后一段再解码，所以带不带前缀都行。
func ImageDataURI(mime string, b []byte) string {
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// UploadImageToCDN 上传图片到微信 CDN（JSON + base64）。
func (u *UploadService) UploadImageToCDN(ctx context.Context, imageBase64 string) (json.RawMessage, error) {
	body := map[string]interface{}{"imageContent": imageBase64}
	var out json.RawMessage
	if err := u.c.call(ctx, "POST", "/message/UploadImageToCDN", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UploadHeadImage 上传头像。
func (u *UploadService) UploadHeadImage(ctx context.Context, imageBase64 string) error {
	return u.c.call(ctx, "POST", "/user/UploadHeadImage", map[string]interface{}{
		"Base64": imageBase64,
	}, nil)
}

// SendVoice 发送语音（base64）。
func (u *UploadService) SendVoice(ctx context.Context, to, voiceBase64 string, second int) error {
	body := map[string]interface{}{
		"ToUserName": to,
		"VoiceData":  voiceBase64,
		"VoiceSecond": second,
	}
	return u.c.call(ctx, "POST", "/message/SendVoice", body, nil)
}

// normalizeBase64 去掉可能的 data URI 前缀，只留纯 base64。
func normalizeBase64(v string) string {
	if i := strings.Index(v, ","); i >= 0 && strings.HasPrefix(v, "data:") {
		return v[i+1:]
	}
	return v
}

var _ = normalizeBase64
