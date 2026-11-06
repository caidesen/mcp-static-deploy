package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	langEN     = "en"
	langZH     = "zh"
	langCookie = "lang"
)

// messages is the UI catalog returned by GET /api/session.
// Slugs, URLs, commands, and MCP tool names are not entries.
var messages = map[string]map[string]string{
	langEN: {
		"brand":                   "Static publish",
		"language":                "Language",
		"sign_out":                "Sign out",
		"sign_in":                 "Sign in",
		"create_account":          "Create the first account",
		"setup_before":            "The first account is created with setupToken from",
		"setup_after":             " DATA_DIR/config.json. Password must be at least 12 characters. Later sign-ins use email and password. Magic-link and password-reset email is not delivered.",
		"email":                   "Email",
		"password":                "Password",
		"setup_token":             "Setup token",
		"create_account_btn":      "Create account",
		"sign_in_lead":            "Sign in with email and password. Magic-link email is not sent.",
		"sites":                   "Sites",
		"signed_in_as":            "Account",
		"site_hint":               "Creating a site gives it a public URL on its own subdomain.",
		"create_site":             "Create site",
		"note":                    "Note",
		"note_placeholder":        "What this site contains",
		"save_note":               "Save note",
		"no_sites":                "No sites yet.",
		"tools_heading":           "Tools",
		"anon_rejected":           "Anonymous calls are rejected.",
		"all_sites":               "All sites",
		"public_url":              "Public URL",
		"local_check":             "Local check, without DNS:",
		"file":                    "File",
		"choose_file":             "Choose file",
		"path_label":              "Path (optional, defaults to the file name)",
		"upload":                  "Upload",
		"delete":                  "Delete",
		"cancel":                  "Cancel",
		"bytes":                   "bytes",
		"no_files_before":         "No files yet. Upload",
		"no_files_after":          "to give the site a front page.",
		"err_sign_in_failed":      "Sign-in failed.",
		"err_weak_password":       "Password must be at least 12 characters.",
		"err_email_taken":         "That email is already registered.",
		"err_invalid_credentials": "Email or password is incorrect.",
		"err_rate_limited":        "Too many attempts. Try again later.",
		"err_account_locked":      "This account is temporarily locked.",
		"err_signup_closed":       "Signup is closed.",
		"err_setup_token":         "Setup token is incorrect.",
		"err_bad_request":         "That request could not be processed.",
		"err_internal":            "Something went wrong.",
		"err_upload_too_large":    "Upload is too large.",
		"err_choose_file":         "Choose a file.",
		"err_bad_path":            "That path is not allowed.",
		"err_not_found":           "File or site not found.",
		"err_too_large":           "File is larger than 8 MiB.",
		"err_update_file":         "Could not update the file.",
		"err_note_too_long":       "Note must be 160 characters or fewer.",
		"copy_link":               "Copy link",
		"copied":                  "Copied",
		"files_label":             "Files",
		"files_heading":           "Files",
		"files_one":               "%d file",
		"files_many":              "%d files",
		"col_name":                "Name",
		"col_size":                "Size",
		"ago_now":                 "Just now",
		"ago_minute":              "%d minute ago",
		"ago_minutes":             "%d minutes ago",
		"ago_hour":                "%d hour ago",
		"ago_hours":               "%d hours ago",
		"ago_day":                 "%d day ago",
		"ago_days":                "%d days ago",
	},
	langZH: {
		"brand":                   "静态发布",
		"language":                "语言",
		"sign_out":                "退出",
		"sign_in":                 "登录",
		"create_account":          "创建第一个账号",
		"setup_before":            "第一个账号使用",
		"setup_after":             " DATA_DIR/config.json 里的 setupToken 创建。密码至少 12 个字符。之后使用邮箱和密码登录。魔法链接和密码重置邮件不会发送。",
		"email":                   "邮箱",
		"password":                "密码",
		"setup_token":             "设置令牌",
		"create_account_btn":      "创建账号",
		"sign_in_lead":            "使用邮箱和密码登录。不会发送魔法链接邮件。",
		"sites":                   "站点",
		"signed_in_as":            "当前账号",
		"site_hint":               "新建站点会得到独立子域名上的公开地址。",
		"create_site":             "新建站点",
		"note":                    "说明",
		"note_placeholder":        "这个站点里有什么",
		"save_note":               "保存说明",
		"no_sites":                "还没有站点。",
		"tools_heading":           "工具",
		"anon_rejected":           "匿名调用会被拒绝。",
		"all_sites":               "全部站点",
		"public_url":              "公开地址",
		"local_check":             "本地检查，无需 DNS：",
		"file":                    "文件",
		"choose_file":             "选择文件",
		"path_label":              "路径（可选，默认使用文件名）",
		"upload":                  "上传",
		"delete":                  "删除",
		"cancel":                  "取消",
		"bytes":                   "字节",
		"no_files_before":         "还没有文件。上传",
		"no_files_after":          "作为首页。",
		"err_sign_in_failed":      "登录失败。",
		"err_weak_password":       "密码至少需要 12 个字符。",
		"err_email_taken":         "该邮箱已注册。",
		"err_invalid_credentials": "邮箱或密码不正确。",
		"err_rate_limited":        "尝试次数过多，请稍后再试。",
		"err_account_locked":      "此账号已暂时锁定。",
		"err_signup_closed":       "注册已关闭。",
		"err_setup_token":         "设置令牌不正确。",
		"err_bad_request":         "无法处理该请求。",
		"err_internal":            "出错了。",
		"err_upload_too_large":    "上传内容过大。",
		"err_choose_file":         "请选择文件。",
		"err_bad_path":            "不允许该路径。",
		"err_not_found":           "找不到文件或站点。",
		"err_too_large":           "文件超过 8 MiB。",
		"err_update_file":         "无法更新文件。",
		"err_note_too_long":       "说明不能超过 160 个字符。",
		"copy_link":               "复制链接",
		"copied":                  "已复制",
		"files_label":             "文件数",
		"files_heading":           "文件列表",
		"files_one":               "%d 个文件",
		"files_many":              "%d 个文件",
		"col_name":                "名称",
		"col_size":                "大小",
		"ago_now":                 "刚刚",
		"ago_minute":              "%d 分钟前",
		"ago_minutes":             "%d 分钟前",
		"ago_hour":                "%d 小时前",
		"ago_hours":               "%d 小时前",
		"ago_day":                 "%d 天前",
		"ago_days":                "%d 天前",
	},
}

func init() {
	if len(messages[langEN]) != len(messages[langZH]) {
		panic("i18n catalogs differ in size")
	}
	for key, en := range messages[langEN] {
		zh, ok := messages[langZH][key]
		if !ok || en == "" || zh == "" {
			panic("i18n missing " + key)
		}
	}
}

func relativeTime(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Minute {
		return text(lang, "ago_now")
	}
	if d < time.Hour {
		n := int(d.Minutes())
		if n < 1 {
			n = 1
		}
		if n == 1 {
			return fmt.Sprintf(text(lang, "ago_minute"), n)
		}
		return fmt.Sprintf(text(lang, "ago_minutes"), n)
	}
	if d < 24*time.Hour {
		n := int(d.Hours())
		if n < 1 {
			n = 1
		}
		if n == 1 {
			return fmt.Sprintf(text(lang, "ago_hour"), n)
		}
		return fmt.Sprintf(text(lang, "ago_hours"), n)
	}
	n := int(d.Hours()) / 24
	if n < 1 {
		n = 1
	}
	if n == 1 {
		return fmt.Sprintf(text(lang, "ago_day"), n)
	}
	return fmt.Sprintf(text(lang, "ago_days"), n)
}

func fileCountText(lang string, n int) string {
	if n == 1 {
		return fmt.Sprintf(text(lang, "files_one"), n)
	}
	return fmt.Sprintf(text(lang, "files_many"), n)
}

func text(lang, key string) string {
	if m, ok := messages[lang]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	if s, ok := messages[langEN][key]; ok {
		return s
	}
	return key
}

// negotiateLang uses the lang cookie when it is zh or en.
// Otherwise the highest-q Accept-Language tag wins: Chinese selects zh, anything else selects en.
func negotiateLang(r *http.Request) string {
	if r != nil {
		if c, err := r.Cookie(langCookie); err == nil {
			if lang, ok := knownLang(c.Value); ok {
				return lang
			}
		}
		if r.Header != nil {
			return langFromAccept(r.Header.Get("Accept-Language"))
		}
	}
	return langEN
}

func knownLang(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case langZH:
		return langZH, true
	case langEN:
		return langEN, true
	default:
		return "", false
	}
}

func langFromAccept(header string) string {
	type pref struct {
		tag string
		q   float64
		i   int
	}
	var prefs []pref
	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if semi := strings.Index(part, ";"); semi >= 0 {
			tag = strings.TrimSpace(part[:semi])
			for _, param := range strings.Split(part[semi+1:], ";") {
				param = strings.TrimSpace(param)
				name, val, ok := strings.Cut(param, "=")
				if ok && strings.EqualFold(strings.TrimSpace(name), "q") {
					if parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
						q = parsed
					}
				}
			}
		}
		prefs = append(prefs, pref{tag: strings.ToLower(tag), q: q, i: i})
	}
	sort.SliceStable(prefs, func(a, b int) bool {
		if prefs[a].q == prefs[b].q {
			return prefs[a].i < prefs[b].i
		}
		return prefs[a].q > prefs[b].q
	})
	if len(prefs) == 0 || prefs[0].q <= 0 {
		return langEN
	}
	tag := prefs[0].tag
	if tag == "zh" || strings.HasPrefix(tag, "zh-") {
		return langZH
	}
	return langEN
}

func langRedirect(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return "/"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host != r.Host || u.Path == "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	if u.Path == "/lang" {
		return "/"
	}
	return u.RequestURI()
}

var authCodeKey = map[string]string{
	"weak_password":       "err_weak_password",
	"email_taken":         "err_email_taken",
	"invalid_credentials": "err_invalid_credentials",
	"rate_limited":        "err_rate_limited",
	"account_locked":      "err_account_locked",
	"signup_closed":       "err_signup_closed",
	"setup_token_invalid": "err_setup_token",
	"bad_request":         "err_bad_request",
	"internal_error":      "err_internal",
}

func authErrorKey(body []byte) string {
	var msg struct {
		Code string `json:"code"`
	}
	if len(body) > 0 && body[0] == '{' {
		_ = json.Unmarshal(body, &msg)
	}
	if key, ok := authCodeKey[msg.Code]; ok {
		return key
	}
	return "err_sign_in_failed"
}
