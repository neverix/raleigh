package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/list"
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

	var m tea.Model

	items := []list.Item{
		menuItem{name: "Start", model: start(m)},
		menuItem{name: "Settings", model: settings(upToDate(func() tea.Model { return m }))},
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

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}

func start(m tea.Model) tea.Model {
	return simpleSpinner(func() tea.Msg {
		tpuWatcher := TPUWatcher{
			project:      viper.GetString("project"),
			zone:         viper.GetString("zone"),
			instanceType: viper.GetString("instanceType"),
			id:           "raleigh-tpu-1",
		}
		status := tpuWatcher.checkStatus()
		if status == tpuStatusRunning || status == tpuStatusCreating {
			delErr := tpuWatcher.delete()
			if delErr != nil {
				panic(fmt.Errorf("error deleting tpu: %w", delErr))
			}
		}
		status = tpuWatcher.checkStatus()
		if status == tpuStatusNonexistent {
			startErr := tpuWatcher.start()
			if startErr != nil {
				panic(fmt.Errorf("error starting tpu: %w", startErr))
			}
		}
		return m
	}, "Starting...")
}
