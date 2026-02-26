package wizard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

// deviceCodeResponse mirrors the wizard package type.
type deviceCodeResponse struct {
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	PollingToken    string `json:"polling_token"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// pollResponse mirrors the wizard package type.
type pollResponse struct {
	Status    string `json:"status"`
	Token     string `json:"token,omitempty"`
	RuntimeID string `json:"runtime_id,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
}

type deviceCodeModel struct {
	data    *WizardData
	spinner spinner.Model

	// State
	phase     string // "requesting", "polling", "approved", "error"
	code      string
	verifyURL string
	pollToken string
	interval  time.Duration
	expiresAt time.Time
	errMsg    string

	browserOpened bool
}

func newDeviceCodeModel(data *WizardData) deviceCodeModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = tui.Selected

	return deviceCodeModel{
		data:    data,
		spinner: sp,
		phase:   "requesting",
	}
}

// Messages
type deviceCodeReceivedMsg struct {
	resp deviceCodeResponse
}

type pollTickMsg struct{}

type pollResultMsg struct {
	resp pollResponse
	err  error
}

type deviceCodeErrorMsg struct {
	err error
}

func (m deviceCodeModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.requestDeviceCode,
	)
}

func (m deviceCodeModel) Update(msg tea.Msg) (deviceCodeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "r":
			if m.phase == "error" {
				m.phase = "requesting"
				m.errMsg = ""
				return m, m.requestDeviceCode
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case deviceCodeReceivedMsg:
		m.phase = "polling"
		m.code = msg.resp.UserCode
		m.verifyURL = msg.resp.VerificationURL
		m.pollToken = msg.resp.PollingToken
		m.interval = time.Duration(msg.resp.Interval) * time.Second
		if m.interval < time.Second {
			m.interval = 5 * time.Second
		}
		m.expiresAt = time.Now().Add(time.Duration(msg.resp.ExpiresIn) * time.Second)

		// Try to open browser.
		if !m.browserOpened {
			m.browserOpened = true
			_ = openBrowser(m.verifyURL)
		}

		return m, tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{} })

	case pollTickMsg:
		if time.Now().After(m.expiresAt) {
			m.phase = "error"
			m.errMsg = "Registration code expired. Press 'r' to retry or 'esc' to go back."
			return m, nil
		}
		return m, m.pollForApproval

	case pollResultMsg:
		if msg.err != nil {
			// Transient error, keep polling.
			return m, tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{} })
		}
		switch msg.resp.Status {
		case "approved":
			m.phase = "approved"
			m.data.Token = msg.resp.Token
			m.data.RuntimeID = msg.resp.RuntimeID
			m.data.OrgID = msg.resp.OrgID
			return m, func() tea.Msg { return stepCompleteMsg{} }
		case "expired":
			m.phase = "error"
			m.errMsg = "Registration code expired. Press 'r' to retry or 'esc' to go back."
			return m, nil
		default:
			// pending — keep polling
			return m, tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{} })
		}

	case deviceCodeErrorMsg:
		m.phase = "error"
		m.errMsg = msg.err.Error()
		return m, nil
	}

	return m, nil
}

func (m deviceCodeModel) View() string {
	s := tui.Subtitle.Render("Device Registration") + "\n\n"

	switch m.phase {
	case "requesting":
		s += "  " + m.spinner.View() + " Requesting registration code...\n"

	case "polling":
		codeBox := tui.CodeBox.Render(
			fmt.Sprintf("Your code:  %s\nOpen:       %s", m.code, m.verifyURL),
		)
		s += codeBox + "\n\n"
		s += "  " + m.spinner.View() + " Waiting for approval in your browser...\n"

	case "approved":
		s += "  " + tui.Success.Render("✓ Registration approved!") + "\n"
		if m.data.RuntimeID != "" {
			s += "  " + tui.Description.Render("Runtime ID: "+m.data.RuntimeID) + "\n"
		}

	case "error":
		s += "  " + tui.ErrorStyle.Render("✗ "+m.errMsg) + "\n"
	}

	s += "\n" + tui.Help.Render("  esc back • r retry")
	return s
}

func (m deviceCodeModel) requestDeviceCode() tea.Msg {
	httpBase := wsToHTTP(m.data.HubURL)

	resp, err := http.Post(httpBase+"/api/runtime/register", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		return deviceCodeErrorMsg{err: fmt.Errorf("request device code: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return deviceCodeErrorMsg{err: fmt.Errorf("device code request failed (HTTP %d)", resp.StatusCode)}
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return deviceCodeErrorMsg{err: fmt.Errorf("hub does not support device-code registration (got %s)", ct)}
	}

	var dcResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return deviceCodeErrorMsg{err: fmt.Errorf("decode device code: %w", err)}
	}

	return deviceCodeReceivedMsg{resp: dcResp}
}

func (m deviceCodeModel) pollForApproval() tea.Msg {
	httpBase := wsToHTTP(m.data.HubURL)

	body, _ := json.Marshal(map[string]string{"polling_token": m.pollToken})
	resp, err := http.Post(httpBase+"/api/runtime/register/poll", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return pollResultMsg{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	var pr pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return pollResultMsg{err: err}
	}
	return pollResultMsg{resp: pr}
}

// wsToHTTP converts a WebSocket URL to its HTTP equivalent.
func wsToHTTP(wsURL string) string {
	u := wsURL
	if strings.HasPrefix(u, "wss://") {
		u = "https://" + strings.TrimPrefix(u, "wss://")
	} else if strings.HasPrefix(u, "ws://") {
		u = "http://" + strings.TrimPrefix(u, "ws://")
	}
	u = strings.TrimSuffix(u, "/ws/runtime")
	return u
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
