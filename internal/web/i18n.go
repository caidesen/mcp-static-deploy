package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	langEN     = "en"
	langZH     = "zh"
	langCookie = "lang"
)

// messages is the UI catalog. Templates call viewData.T with these keys.
// Slugs, URLs, commands, and MCP tool names are not entries.
var messages = map[string]map[string]string{
	langEN: {
		"brand":                   "Static publish",
		"language":                "Language",
		"sign_out":                "Sign out",
		"sign_in":                 "Sign in",
		"create_account":          "Create the first account",
		"setup_before":            "The first account is created with the setup token from",
		"setup_after":             ". Password must be at least 12 characters. Later sign-ins use email and password. Magic-link and password-reset email is not delivered.",
		"email":                   "Email",
		"password":                "Password",
		"setup_token":             "Setup token",
		"create_account_btn":      "Create account",
		"sign_in_lead":            "Sign in with email and password. Magic-link email is not sent.",
		"sites":                   "Sites",
		"signed_in_as":            "Signed in as",
		"site_hint":               "Creating a site gives it a public URL on its own subdomain.",
		"create_site":             "Create site",
		"no_sites":                "No sites yet.",
		"api_token_heading":       "API token for MCP",
		"mcp_before":              "Agents call",
		"mcp_after_url":           "with",
		"mcp_after_auth":          "and the token below. A browser session cookie is also accepted. The token can publish with the",
		"mcp_after_ability":       "ability and expires in 90 days. The secret is shown once.",
		"copy_token":              "Copy this token now. It will not be shown again.",
		"token_name":              "Token name",
		"create_token":            "Create API token",
		"until":                   "until",
		"tools_heading":           "Tools",
		"anon_rejected":           "Anonymous calls are rejected.",
		"all_sites":               "All sites",
		"public_url":              "Public URL",
		"local_check":             "Local check, without DNS:",
		"file":                    "File",
		"path_label":              "Path (optional, defaults to the file name)",
		"upload":                  "Upload",
		"delete":                  "Delete",
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
		"err_token":               "Could not create the token.",
	},
	langZH: {
		"brand":                   "静态发布",
		"language":                "语言",
		"sign_out":                "退出",
		"sign_in":                 "登录",
		"create_account":          "创建第一个账号",
		"setup_before":            "第一个账号使用环境变量",
		"setup_after":             " 中的设置令牌创建。密码至少 12 个字符。之后使用邮箱和密码登录。魔法链接和密码重置邮件不会发送。",
		"email":                   "邮箱",
		"password":                "密码",
		"setup_token":             "设置令牌",
		"create_account_btn":      "创建账号",
		"sign_in_lead":            "使用邮箱和密码登录。不会发送魔法链接邮件。",
		"sites":                   "站点",
		"signed_in_as":            "当前登录",
		"site_hint":               "新建站点会得到独立子域名上的公开地址。",
		"create_site":             "新建站点",
		"no_sites":                "还没有站点。",
		"api_token_heading":       "MCP API 令牌",
		"mcp_before":              "代理请求",
		"mcp_after_url":           "，使用",
		"mcp_after_auth":          "并附带下面的令牌。浏览器会话 Cookie 同样可用。令牌具备",
		"mcp_after_ability":       "能力，90 天后过期。密钥只显示一次。",
		"copy_token":              "请立即复制此令牌。它不会再次显示。",
		"token_name":              "令牌名称",
		"create_token":            "创建 API 令牌",
		"until":                   "至",
		"tools_heading":           "工具",
		"anon_rejected":           "匿名调用会被拒绝。",
		"all_sites":               "全部站点",
		"public_url":              "公开地址",
		"local_check":             "本地检查，无需 DNS：",
		"file":                    "文件",
		"path_label":              "路径（可选，默认使用文件名）",
		"upload":                  "上传",
		"delete":                  "删除",
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
		"err_token":               "无法创建令牌。",
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

func (v viewData) T(key string) string {
	return text(v.Lang, key)
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
