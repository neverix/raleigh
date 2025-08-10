package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/viper"
)

const listHeight = 6

var (
	titleStyle      = lipgloss.NewStyle().MarginLeft(2)
	paginationStyle = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle       = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)
	quitTextStyle   = lipgloss.NewStyle().Margin(1, 0, 2, 4)
)

type menuItem struct {
	name  string
	model tea.Model
}

func (i menuItem) Title() string       { return i.name }
func (i menuItem) Description() string { return "" }
func (i menuItem) FilterValue() string { return i.name }

type model struct {
	list     list.Model
	choice   string
	quitting bool
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height - 1)
		return m, nil

	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "enter":
			i, ok := m.list.SelectedItem().(menuItem)
			if ok {
				m.choice = i.name
			}
			return i.model, i.model.Init()
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return quitTextStyle.Render("Goodbye.")
	}
	return m.list.View()
}

type GcloudAuth struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

func main() {
	gcloudAuth, err := exec.Command("gcloud", "auth", "list", "--format", "json").Output()
	if err != nil {
		panic(fmt.Errorf("fatal error getting gcloud auth: %w", err))
	}
	var authList []GcloudAuth
	err = json.Unmarshal(gcloudAuth, &authList)
	if err != nil {
		panic(fmt.Errorf("fatal error unmarshalling gcloud auth: %w", err))
	}
	hasActiveAuth := false
	for _, auth := range authList {
		if auth.Status == "ACTIVE" {
			hasActiveAuth = true
		}
	}
	if !hasActiveAuth {
		exec.Command("gcloud", "auth", "login").Run()
	}

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/raleigh/")
	viper.AddConfigPath("$HOME/.raleigh")

	err = viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			fmt.Println("Config file not found; creating default config file")
			home_dir, err := os.UserHomeDir()
			if err != nil {
				panic(fmt.Errorf("fatal error getting home directory: %w", err))
			}
			os.MkdirAll(fmt.Sprintf("%s/.raleigh", home_dir), 0755)
			viper.SafeWriteConfigAs(fmt.Sprintf("%s/.raleigh/config.yaml", home_dir))
		} else {
			panic(fmt.Errorf("fatal error config file: %w", err))
		}
	}

	viper.SetDefault("spot", false)
	viper.SetDefault("preemptible", false)
	viper.SetDefault("instanceType", "v4-8")
	viper.SetDefault("tpuPrefix", "hobby")
	viper.SetDefault("numTpus", 2)
	viper.SetDefault("username", "raleigh")
	viper.SetDefault("repoPath", "./jif")
	viper.SetDefault("remoteRepoPath", "~/jif")
	viper.SetDefault("installCommand", "~/.local/bin/uv sync")
	viper.SetDefault("installerVersion", "0.0.1b")
	viper.SetDefault("runCommand", "~/.local/bin/uv run -m jif")

	var m tea.Model

	mUpToDate := upToDate(func() tea.Model { return m })
	items := []list.Item{
		menuItem{name: "Start", model: start(mUpToDate)},
		menuItem{name: "Settings", model: settings(mUpToDate)},
	}

	const defaultWidth = 20

	delegate := list.NewDefaultDelegate()
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.PaddingTop(1)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.PaddingTop(1)

	l := list.New(items, delegate, defaultWidth, listHeight-1)
	l.Title = "Raleigh"
	l.SetShowStatusBar(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle

	m = &model{list: l}

	if viper.GetString("project") == "" {
		m = selectProject(m)
	}
	if viper.GetString("region") == "" {
		m = selectRegion(m)
	}
	if viper.GetString("instanceType") == "" {
		m = selectInstanceType(m)
	}

	if os.Getenv("AUTORUN") == "1" {
		m = start(m)
	}

	if _, err := tea.NewProgram(ResizeWrapper{m}).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}

type tpuStats struct {
	numActive     int
	numInstalled  int
	numCloned     int
	numRunning    int
	latestError   error
	latestErrorId int
}

type TpuLaunchMonitor struct {
	watcher  *TpuWatcher
	tpuStats tpuStats
	viewport viewport.Model
}

func listenTpuUpdates(watcher *TpuWatcher) tea.Cmd {
	return func() tea.Msg {
		update := <-watcher.updates
		numActive, numInstalled, numCloned, numRunning := 0, 0, 0, 0
		latestError := error(nil)
		latestErrorId := -1
		if update.err != nil {
			latestError = update.err
			latestErrorId = update.id + 1
		}
		for i := range len(watcher.statuses) {
			status := &watcher.statuses[i]
			status.mutex.Lock()
			if status.status.status == tpuStatusRunning {
				numActive++
			}
			if status.status.installed {
				numInstalled++
			}
			if status.status.cloned {
				numCloned++
			}
			if status.status.running {
				numRunning++
			}
			status.mutex.Unlock()
		}
		return tpuStats{
			numActive:     numActive,
			numInstalled:  numInstalled,
			numCloned:     numCloned,
			numRunning:    numRunning,
			latestError:   latestError,
			latestErrorId: latestErrorId,
		}
	}
}

func (t *TpuLaunchMonitor) Init() tea.Cmd {
	return listenTpuUpdates(t.watcher)
}

func (t *TpuLaunchMonitor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.viewport.Width = msg.Width
		t.viewport.Height = msg.Height - 6
		return t, nil

	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "q", "ctrl+c":
			return nil, tea.Quit
		}
	case tpuStats:
		t.tpuStats = msg
		if t.tpuStats.latestError != nil {
			errorStr := fmt.Sprintf("Error: \"%s\" (TPU %d)", strings.ReplaceAll(t.tpuStats.latestError.Error(), "\n", "\\n"), t.tpuStats.latestErrorId)
			t.viewport.SetContent(lipgloss.NewStyle().Width(t.viewport.Width).Render(errorStr))
		} else {
			t.viewport.SetContent("")
		}
		return t, listenTpuUpdates(t.watcher)
	}
	var cmd tea.Cmd
	t.viewport, cmd = t.viewport.Update(msg)
	return t, cmd
}

func (t *TpuLaunchMonitor) View() string {
	builder := strings.Builder{}
	builder.WriteString(lipgloss.NewStyle().Width(t.viewport.Width).Border(lipgloss.NormalBorder()).Padding(1).Render(t.viewport.View()))
	statsStr := fmt.Sprintf("Active: %d, Installed: %d, Cloned: %d, Running: %d", t.tpuStats.numActive, t.tpuStats.numInstalled, t.tpuStats.numCloned, t.tpuStats.numRunning)
	builder.WriteString(lipgloss.NewStyle().Width(t.viewport.Width).Border(lipgloss.NormalBorder()).Padding(1).Render(statsStr))
	return builder.String()
}

func start(m tea.Model) tea.Model {
	return simpleSpinner(func() tea.Msg {
		watcher := NewTpuWatcher(GetConfig())

		return &TpuLaunchMonitor{
			watcher: watcher,
		}
	}, "Starting...")
}
