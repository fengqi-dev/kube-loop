package oauthserver

import (
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

// writeDesktopAuthorizationComplete keeps the browser on a useful completion
// page while handing the authorization response to the registered desktop
// protocol. A direct 303 to a custom protocol launches the app, but browsers
// retain the preceding authorization form in the tab.
func writeDesktopAuthorizationComplete(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	authorizeRequest fosite.AuthorizeRequester,
	response fosite.AuthorizeResponder,
) bool {
	if authorizeRequest.GetClient().
		GetID() !=
		controlstorage.DesktopOAuthClientID ||
		authorizeRequest.GetResponseMode() != fosite.ResponseModeDefault &&
			authorizeRequest.GetResponseMode() != fosite.ResponseModeQuery {
		return false
	}
	redirect := authorizeRequest.GetRedirectURI()
	if redirect == nil ||
		redirect.String() != controlstorage.DesktopOAuthRedirectURI {
		return false
	}
	callback := *redirect
	query := cloneValues(callback.Query())
	for key, values := range response.GetParameters() {
		query[key] = append([]string(nil), values...)
	}
	callback.RawQuery = query.Encode()

	header := rw.Header()
	for key, values := range response.GetHeader() {
		header[key] = append([]string(nil), values...)
	}
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set(
		"Content-Security-Policy",
		"default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'",
	)
	header.Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(http.StatusOK)

	target := html.EscapeString(callback.String())
	messages := desktopAuthorizationMessagesFor(
		desktopAuthorizationLocale(httpRequest),
	)
	_, _ = rw.Write([]byte(`<!doctype html>
<html lang="` + messages.lang + `">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="0;url=` + target + `">
  <title>` + messages.title + ` · KubeLoop</title>
  <link rel="stylesheet" href="/oauth2/ui/app.css">
</head>
<body>
  <main>
    <section class="card completion-card">
      <header class="completion-header">
        <div class="brand"><span>KL</span>KubeLoop</div>
        <span class="completion-badge">` + messages.badge + `</span>
      </header>
      <div class="completion-status" aria-hidden="true">
		<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"
		  stroke-linecap="round" stroke-linejoin="round">
          <path d="m5 12 4 4L19 6"></path>
        </svg>
      </div>
      <h1 class="completion-title">` + messages.heading + `</h1>
      <div class="completion-copy">
        <p>` + messages.description + `</p>
      </div>
      <div class="completion-actions">
        <a class="completion-button primary" href="` + target + `">
          <span>` + messages.returnToApp + `</span>
		  <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor"
		    stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M5 12h14"></path><path d="m13 6 6 6-6 6"></path>
          </svg>
        </a>
        <form class="completion-logout" method="post" action="/oauth2/logout">
          <button type="submit" class="secondary">
			<svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor"
			  stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
			  <path d="M10 17l5-5-5-5"></path>
			  <path d="M15 12H3"></path>
			  <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"></path>
            </svg>
            <span>` + messages.logout + `</span>
          </button>
        </form>
      </div>
      <p class="completion-note">` + messages.logoutNote + `</p>
    </section>
  </main>
</body>
</html>`))
	return true
}

type desktopAuthorizationMessages struct {
	lang        string
	title       string
	badge       string
	heading     string
	description string
	returnToApp string
	logout      string
	logoutNote  string
}

func desktopAuthorizationMessagesFor(
	locale string,
) desktopAuthorizationMessages {
	if locale == localeChineseSimplified {
		return desktopAuthorizationMessages{
			lang:        localeChineseSimplified,
			title:       loginCompleteTitleChinese,
			badge:       "安全登录",
			heading:     loginCompleteTitleChinese,
			description: "KubeLoop 桌面应用已收到授权结果，现在可以安全地关闭此页面。",
			returnToApp: "返回 KubeLoop",
			logout:      "退出登录",
			logoutNote:  "仅退出当前浏览器会话",
		}
	}
	return desktopAuthorizationMessages{
		lang: "en", title: loginCompleteTitle, badge: "Secure sign-in", heading: loginCompleteTitle,
		description: "KubeLoop Desktop has received the authorization result. You can safely close this tab.",
		returnToApp: "Return to KubeLoop", logout: "Log out", logoutNote: "Logs out this browser session only",
	}
}

func desktopAuthorizationLocale(request *http.Request) string {
	if request == nil {
		return localeEnglishUS
	}
	if locale := request.FormValue("locale"); locale == localeChineseSimplified ||
		locale == localeEnglishUS {
		return locale
	}
	if cookie, err := request.Cookie("kubeloop.locale"); err == nil &&
		(cookie.Value == localeChineseSimplified || cookie.Value == localeEnglishUS) {
		return cookie.Value
	}
	preferred := strings.ToLower(
		strings.TrimSpace(
			strings.Split(request.Header.Get("Accept-Language"), ",")[0],
		),
	)
	if strings.HasPrefix(preferred, "zh") {
		return localeChineseSimplified
	}
	return localeEnglishUS
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}
