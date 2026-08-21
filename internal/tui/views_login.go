package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

// updateLogin handles key presses in the login view.
func (m Model) updateLogin(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.loginAdding {
		return m.updateAddProfile(msg)
	}
	if msg.String() == keyEsc && m.loginCancel != nil {
		m.loginCancel()
		m.loginCancel = nil
		m.loading = false
		m.err, m.status = "", "Cancelling login..."
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.loginCursor > 0 {
			m.loginCursor--
		}
	case keyDown, "j":
		if m.loginCursor < len(m.profiles.Profiles)-1 {
			m.loginCursor++
		}
	case keyEnter:
		if len(m.profiles.Profiles) == 0 || m.loginCursor >= len(m.profiles.Profiles) {
			return m, nil
		}
		profile := m.profiles.Profiles[m.loginCursor]
		m.activeProfile = profile
		if err := m.state.profiles.SetActive(profile.ID); err != nil {
			m.err, m.status = err.Error(), ""
			return m, nil
		}
		m.profiles = m.state.Snapshot()
		m.loading, m.profileSelectionPending = true, true
		m.status = fmt.Sprintf("Opening: %s", firstNonEmpty(profile.DisplayName, profile.ID))
		return m, loadAuthStatus(m)

	case "a", "n":
		m.loginAdding = true
		m.loginURL = ""
		m.err = ""
		return m, nil

	case "d":
		if len(m.profiles.Profiles) == 0 || m.loginCursor >= len(m.profiles.Profiles) {
			return m, nil
		}
		profile := m.profiles.Profiles[m.loginCursor]
		m.loading = true
		return m, m.deleteProfile(profile.ID)

	case "l":
		if len(m.profiles.Profiles) == 0 || m.loginCursor >= len(m.profiles.Profiles) {
			return m, nil
		}
		profile := m.profiles.Profiles[m.loginCursor]
		m.activeProfile = profile
		_ = m.state.profiles.SetActive(profile.ID)
		loginContext, cancel := context.WithCancel(m.state.ctx)
		m.loginCancel = cancel
		m.loading = true
		m.err, m.status = "", "Waiting for browser login... Press Esc to cancel"
		return m, tea.Batch(m.spinner.Tick, m.startLogin(loginContext))

	case "L":
		return m, m.beginLogout()
	}

	return m, nil
}

func (m *Model) beginLogout() tea.Cmd {
	if m.connected() {
		m.err, m.status = errDisconnectBeforeLogout, ""
		return nil
	}
	if m.activeProfile.ID == "" || !m.authSession.Authenticated {
		m.err, m.status = "", "Not logged in"
		return nil
	}
	m.loading = true
	m.err, m.status = "", "Logging out..."
	return tea.Batch(m.spinner.Tick, m.logout())
}

// updateAddProfile handles key presses in the add profile form.
func (m Model) updateAddProfile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.loading && msg.String() != keyEsc {
		return m, nil
	}
	switch msg.String() {
	case keyEnter:
		m.loginURL = strings.TrimSpace(m.loginURL)
		if m.loginURL == "" {
			m.err = "Service address is required"
			return m, nil
		}
		m.loading = true
		m.err, m.status = "", "Discovering server..."
		return m, tea.Batch(m.spinner.Tick, m.saveProfile())

	case keyEsc:
		m.loginAdding = false
		m.loginURL = ""
		m.err = ""
		return m, nil

	case keyBackspace:
		m.loginURL = trimLastRune(m.loginURL)
		return m, nil

	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			m.loginURL += string(msg.Runes)
		}
		return m, nil
	}
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

// startLogin creates a command to start the OIDC login flow.
func (m Model) startLogin(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		document, err := m.state.discovery.Discover(ctx, m.activeProfile.BaseURL)
		if err != nil {
			return loginResultMsg{
				err:       fmt.Errorf("discover authentication providers: %w", err),
				cancelled: errors.Is(ctx.Err(), context.Canceled),
			}
		}
		if len(document.AuthMethods) == 0 {
			return loginResultMsg{err: errors.New("this server has no login method configured")}
		}
		providerID := document.AuthMethods[0].ID
		cred, err := m.state.auth.LoginOIDC(ctx, m.activeProfile.BaseURL, providerID, m.activeProfile.ID)
		if err != nil {
			return loginResultMsg{err: err, cancelled: errors.Is(ctx.Err(), context.Canceled)}
		}
		if err := m.state.credentials.Set(m.activeProfile.ID, cred); err != nil {
			return loginResultMsg{err: err}
		}
		session, _ := m.state.AuthStatus(m.activeProfile.ID)
		return loginResultMsg{session: session}
	}
}

// logout creates a command to logout and revoke credentials.
func (m Model) logout() tea.Cmd {
	return func() tea.Msg {
		cred, err := m.state.credentials.Get(m.activeProfile.ID)
		if err == nil {
			_ = m.state.auth.Revoke(m.state.ctx, m.activeProfile.BaseURL, cred.RefreshToken)
		}
		_ = m.state.credentials.Delete(m.activeProfile.ID)
		return logoutResultMsg{}
	}
}

// saveProfile creates a command to save a new profile via server discovery.
func (m Model) saveProfile() tea.Cmd {
	return func() tea.Msg {
		document, err := m.state.discovery.Discover(m.state.ctx, m.loginURL)
		if err != nil {
			return profileSavedMsg{err: fmt.Errorf("discover server: %w", err)}
		}
		baseURL, err := requestedServerBaseURL(m.loginURL)
		if err != nil {
			return profileSavedMsg{err: err}
		}
		profile := clientprofile.Profile{
			ID:          document.ServiceID,
			BaseURL:     baseURL,
			TunnelPath:  document.TunnelPath,
			DisplayName: document.ServiceID,
		}
		if err := m.state.profiles.Upsert(profile); err != nil {
			return profileSavedMsg{err: err}
		}
		_ = m.state.profiles.SetActive(profile.ID)
		return profileSavedMsg{profile: profile}
	}
}

// requestedServerBaseURL validates and returns the requested server URL.
func requestedServerBaseURL(requestedValue string) (string, error) {
	return clientprofile.NormalizeBaseURL(requestedValue)
}

// deleteProfile creates a command to delete a profile.
func (m Model) deleteProfile(profileID string) tea.Cmd {
	return func() tea.Msg {
		_ = m.state.credentials.Delete(profileID)
		if err := m.state.profiles.Remove(profileID); err != nil {
			return profileDeletedMsg{err: err}
		}
		return profileDeletedMsg{state: m.state.Snapshot()}
	}
}
