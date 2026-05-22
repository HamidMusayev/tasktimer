package ui

import (
	"strings"
	"time"

	"github.com/caarlos0/tasktimer/internal/model"
	"github.com/caarlos0/tasktimer/internal/store"
	timeago "github.com/caarlos0/timea.go"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dgraph-io/badger/v3"
)

type keymap struct {
	Esc   key.Binding
	Enter key.Binding
	CtrlC key.Binding
	R key.Binding
}

func Init(db *badger.DB, project string) tea.Model {
	input := textinput.NewModel()
	input.Prompt = "❯ "
	input.Placeholder = "New task description..."
	input.Focus()
	input.CharLimit = 250
	input.Width = 50

	keymap := &keymap{
		Esc: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "start/stop timer"),
		),
		CtrlC: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "exit"),
		),
		R: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "restart"),
		),
	}

	l := list.NewModel([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.Title = "tasks"
	l.SetSpinner(spinner.Pulse)
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			keymap.Esc,
			keymap.Enter,
			keymap.CtrlC,
			keymap.R,
		}
	}

	return mainModel{
		list:    l,
		timer:   projectTimerModel{},
		db:      db,
		input:   input,
		project: project,
		keymap:  keymap,
	}
}

type mainModel struct {
	input   textinput.Model
	list    list.Model
	timer   projectTimerModel
	db      *badger.DB
	project string
	err     error
	keymap  *keymap
}

func (m mainModel) Init() tea.Cmd {
	return tea.Batch(
		m.list.StartSpinner(),
		enqueueTaskListUpdate,
		textinput.Blink,
	)
}

func (m mainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	var newMsg tea.Msg

	m.list.DisableQuitKeybindings()
	m.list.KeyMap.CursorUp.SetEnabled(!m.input.Focused() && !m.list.SettingFilter())
	m.list.KeyMap.CursorDown.SetEnabled(!m.input.Focused() && !m.list.SettingFilter())
	m.list.KeyMap.Filter.SetEnabled(!m.input.Focused() && !m.list.SettingFilter())
	m.keymap.Esc.SetEnabled(m.input.Focused())

	switch msg := msg.(type) {
	case errMsg:
		m.err = msg.error
	case tea.WindowSizeMsg:
		top, right, bottom, left := listStyle.GetMargin()
		m.list.SetSize(msg.Width-left-right, msg.Height-top-bottom)
	case updateTaskListMsg:
		cmds = append(cmds, m.list.StartSpinner(), updateTaskListCmd(m.db))
	case taskListUpdatedMsg:
		items := make([]list.Item, 0, len(msg.tasks))
		for _, t := range msg.tasks {
			items = append(items, item{
				title: t.Title,
				start: t.StartAt,
				end:   t.EndAt,
			})
		}

		m.list.StopSpinner()
		m.list.ResetSelected()
		m.list.ResetFilter()
		cmds = append(cmds, m.list.SetItems(items), updateProjectTimerCmd(msg.tasks))
	case tea.KeyMsg:
		if key.Matches(msg, m.keymap.CtrlC) {
			return m, tea.Sequentially(closeTasksCmd(m.db), tea.Quit)
		}

		if m.list.SettingFilter() {
			break
		}

		if m.input.Focused() {
			if key.Matches(msg, m.keymap.Esc) {
				m.input.Blur()
				cmds = append(cmds, tea.Sequentially(
					closeTasksCmd(m.db),
					updateTaskListCmd(m.db)),
				)
			}
			if key.Matches(msg, m.keymap.Enter) {
				input := strings.TrimSpace(m.input.Value())
				if input != "" {
					cmds = append(cmds, tea.Sequentially(
						closeTasksCmd(m.db),
						createTaskCmd(m.db, input),
					))
				}
				m.input.SetValue("")
			}

			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			newMsg = doNotPropagateMsg{}
		} else {
			if key.Matches(msg, m.keymap.Esc) {
				newMsg = doNotPropagateMsg{}
			}
			if key.Matches(msg, m.keymap.Enter) {
				m.input.Focus()
				cmds = append(cmds, textinput.Blink)
			}
			if key.Matches(msg, m.keymap.R) {
				if m.list.SelectedItem() != nil {
					m.input.SetValue(m.list.SelectedItem().FilterValue())
					m.input.Focus()
					cmds = append(cmds, textinput.Blink)
				}
				newMsg = doNotPropagateMsg{}
			}
		}
	}

	if newMsg != nil {
		msg = newMsg
	}

	m.timer, cmd = m.timer.Update(msg)
	cmds = append(cmds, cmd)
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.list, cmd = m.list.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m mainModel) View() string {
	if m.err != nil {
		return "\n" +
			errorFaintForeground.Render("Oops, something went wrong:") +
			"\n\n" +
			errorForegroundPadded.Render(m.err.Error()) +
			"\n\n" +
			errorFaintForeground.Render("Check the logs for more details...")
	}
	return secondaryForeground.Render("project: ") +
		activeForegroundBold.Render(m.project) +
		separator + m.timer.View() + "\n\n" +
		m.input.View() + "\n\n" +
		m.list.View() + "\n"
}

// msgs

type doNotPropagateMsg struct{}

type updateTaskListMsg struct{}

type taskListUpdatedMsg struct {
	tasks []model.Task
}

type errMsg struct{ error }

func (e errMsg) Error() string { return e.error.Error() }

// cmds

func closeTasksCmd(db *badger.DB) tea.Cmd {
	return func() tea.Msg {
		if err := store.CloseTasks(db); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func createTaskCmd(db *badger.DB, t string) tea.Cmd {
	return func() tea.Msg {
		if err := store.CreateTask(db, t); err != nil {
			return errMsg{err}
		}
		return updateTaskListMsg{}
	}
}

func enqueueTaskListUpdate() tea.Msg {
	return updateTaskListMsg{}
}

func updateTaskListCmd(db *badger.DB) tea.Cmd {
	return func() tea.Msg {
		tasks, err := store.GetTaskList(db)
		if err != nil {
			return errMsg{err}
		}
		return taskListUpdatedMsg{tasks}
	}
}

// models

type item struct {
	title      string
	start, end time.Time
}

func (i item) Title() string {
	if i.end.IsZero() {
		return boldStyle.Render(i.title)
	}
	return i.title
}

func (i item) Description() string {
	end := time.Now()
	if !i.end.IsZero() {
		end = i.end
	}
	ago := timeago.Of(i.start, timeago.Options{
		Precision: timeago.MinutePrecision,
	})
	return ago + " - " + end.Sub(i.start).Round(time.Second).String()
}

func (i item) FilterValue() string { return i.title }
