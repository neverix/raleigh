package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/viper"
)

type instantUpdate struct{}

func mkInstantUpdate() tea.Msg {
	return instantUpdate{}
}

type upToDateModel struct {
	fn func() tea.Model
}

func (m upToDateModel) Init() tea.Cmd {
	return mkInstantUpdate
}

func (m upToDateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.fn(), nil
}

func (m upToDateModel) View() string {
	return ""
}

func upToDate(fn func() tea.Model) tea.Model {
	return upToDateModel{fn: fn}
}

type simpleListItem struct {
	name string
	id   string
}

func (i simpleListItem) Title() string       { return i.name }
func (i simpleListItem) Description() string { return "" }
func (i simpleListItem) FilterValue() string { return i.id }

func setDefault(items []simpleListItem, defaultId string) []list.Item {
	newItems := make([]list.Item, len(items))
	selectedIndex := 0
	for i, item := range items {
		newItems[i] = item
		if item.id == defaultId {
			selectedIndex = i
		}
	}
	previous := items[selectedIndex]
	newItems[selectedIndex] = items[0]
	newItems[0] = previous
	return newItems
}

type simpleListModel struct {
	list     list.Model
	callback func(id string) tea.Model
}

func (m simpleListModel) Init() tea.Cmd { return nil }

func (m simpleListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height - 1)
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.list.SelectedItem() != nil {
				nextModel := m.callback(m.list.SelectedItem().FilterValue())
				return nextModel, nextModel.Init()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m simpleListModel) View() string {
	return m.list.View()
}

func createList(items []list.Item, title string, callback func(id string) tea.Model) simpleListModel {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.PaddingTop(1)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.PaddingTop(1)
	l := list.New(items, delegate, 20, 6)
	l.Title = title
	l.SetShowStatusBar(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return simpleListModel{list: l, callback: callback}
}

type gotNextModel tea.Model

type simpleSpinnerModel struct {
	modelGetter tea.Cmd
	spinner     spinner.Model
	message     string
	height      int
}

func (m simpleSpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.modelGetter)
}

func (m simpleSpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case gotNextModel:
		return msg, msg.Init()
	case spinnerError:
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.height = msg.Height - 1
	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m simpleSpinnerModel) View() string {
	builder := strings.Builder{}
	totalHeight := m.height - 1
	if totalHeight < 0 {
		totalHeight = 0
	}
	heightBefore := totalHeight / 2
	heightAfter := totalHeight - heightBefore
	builder.WriteString(strings.Repeat("\n", heightBefore))
	builder.WriteString("     ")
	builder.WriteString(m.spinner.View())
	builder.WriteString("  ")
	builder.WriteString("\n")
	builder.WriteString(strings.Repeat("\n", heightAfter))
	return builder.String()
}

func simpleSpinner(modelGetter tea.Cmd, message string) simpleSpinnerModel {
	spinnerDot := spinner.New()
	spinnerDot.Spinner = spinner.Dot
	spinnerDot.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return simpleSpinnerModel{modelGetter: modelGetter, spinner: spinnerDot, message: message}
}

type gcloudProject struct {
	Name string `json:"name"`
	ID   string `json:"projectId"`
}

type spinnerError struct {
	err error
}

func (e spinnerError) Error() string {
	return e.err.Error()
}

func selectProject(m tea.Model) tea.Model {
	project := viper.GetString("project")

	var modelGetter tea.Cmd = func() tea.Msg {
		projects, err := exec.Command("gcloud", "projects", "list", "--format", "json").Output()

		if err != nil {
			return spinnerError{err: fmt.Errorf("failed to get projects: %w", err)}
		}
		var items []gcloudProject
		err = json.Unmarshal(projects, &items)
		if err != nil {
			return spinnerError{err: fmt.Errorf("failed to unmarshal projects: %w", err)}
		}

		projectItems := make([]simpleListItem, len(items))
		for i, item := range items {
			projectItems[i] = simpleListItem{name: item.Name, id: item.ID}
		}

		l := createList(setDefault(projectItems, project), "Select Project", func(id string) tea.Model {
			viper.Set("project", id)
			viper.WriteConfig()
			return m
		})
		return gotNextModel(l)
	}
	return simpleSpinner(modelGetter, "Loading projects...")
}

func selectRegion(m tea.Model) tea.Model {
	region := viper.GetString("region")

	items := []simpleListItem{
		simpleListItem{name: "us-central2-b", id: "us-central2-b"},
		simpleListItem{name: "us-central1-f", id: "us-central1-f"},
		simpleListItem{name: "europe-west4-a", id: "europe-west4-a"},
		simpleListItem{name: "us-east1-d", id: "us-east1-d"},
	}
	return createList(setDefault(items, region), "Select Region", func(id string) tea.Model {
		viper.Set("region", id)
		viper.WriteConfig()
		return m
	})
}

func selectInstanceType(m tea.Model) tea.Model {
	instanceType := viper.GetString("instanceType")

	items := []simpleListItem{
		simpleListItem{name: "v2-8", id: "v2-8"},
		simpleListItem{name: "v3-8", id: "v3-8"},
	}
	return createList(setDefault(items, instanceType), "Select Instance Type", func(id string) tea.Model {
		viper.Set("instanceType", id)
		viper.WriteConfig()
		return m
	})
}

func settings(m tea.Model) tea.Model {
	var settings simpleListModel

	settings = createList(
		[]list.Item{
			simpleListItem{name: "Project", id: "project"},
			simpleListItem{name: "Region", id: "region"},
			simpleListItem{name: "Instance Type", id: "instanceType"},
			simpleListItem{name: "Back", id: "back"},
		},
		"Settings",
		func(id string) tea.Model {
			switch id {
			case "project":
				return selectProject(upToDate(func() tea.Model { return settings }))
			case "region":
				return selectRegion(upToDate(func() tea.Model { return settings }))
			case "back":
				return m
			}
			return m
		},
	)
	return settings
}
