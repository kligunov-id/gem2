package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"

	textinput "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	toml "github.com/pelletier/go-toml/v2"
	excelize "github.com/xuri/excelize/v2"
)

type exitCode int

const (
	// Exit codes must not change between versions,
	// only new ones can be added
	// This is also why we do not use iota here
	// as this would prevent accidental renumbering
	ok                   exitCode = 0
	databaseError        exitCode = 1
	loggingError         exitCode = 2
	teaError             exitCode = 3
	internalError        exitCode = 4
	mistakesLoggingError exitCode = 5
	statisticsError      exitCode = 6
)

func exit(code exitCode) {
	os.Exit(int(code))
}

type wordDatabase struct {
	formClue  []string
	verbs     []string
	verbForms [][]string
}

func read_database() wordDatabase {
	table, err := excelize.OpenFile("./words.xlsx")
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(databaseError)
	}
	defer func() {
		if err := table.Close(); err != nil {
			log.Printf("[FATAL] %v\n", err)
			exit(databaseError)
		}
	}()

	sheets := table.GetSheetList()
	dataSheet := sheets[0]
	rows, err := table.GetRows(dataSheet)
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(databaseError)
	}
	if len(rows) < 2 {
		log.Println("[FATAL] Table containts less than 2 lines!")
		exit(databaseError)
	}
	var pronouns []string
	verbs := make([]string, len(rows)-1)
	verbForms := make([][]string, len(rows)-1)
	for row_index, row := range rows {
		if row_index == 0 {
			pronouns = make([]string, len(row)-2)
			copy(pronouns, row[2:])
			continue
		}
		verbForms[row_index-1] = make([]string, len(row)-2)
		for col_index, cell := range row {
			if col_index == 1 {
				verbs[row_index-1] = cell
			}
			if col_index >= 2 {
				verbForms[row_index-1][col_index-2] = cell
			}
		}
	}
	return wordDatabase{
		pronouns,
		verbs,
		verbForms,
	}
}

type questionStats struct {
	streak   uint16
	correct  uint16
	mistakes uint16
}

func (stats questionStats) probWeight() float32 {
	return 1 / (1 + float32(stats.streak))
}

type statisticsDatabase struct {
	statistics      map[prompt]questionStats
	answers         map[prompt]string
	totalProbWeight float32
}

const statisticsPromptSeparator = "+"

func (prompt prompt) Encode() string {
	return fmt.Sprintf("%s%s%s", prompt.formClue, statisticsPromptSeparator, prompt.Verb)
}

type promptDataTOML struct {
	Streak   uint16
	Correct  uint16
	Mistakes uint16
	Answer   string
}

type statisticsDatabaseTOML struct {
	Statistics map[string]promptDataTOML
}

func (statisticsTOML statisticsDatabaseTOML) unpack() statisticsDatabase {
	statistics := map[prompt]questionStats{}
	answers := map[prompt]string{}
	totalProbWeight := float32(0)
	for encodedPrompt, data := range statisticsTOML.Statistics {
		prompt_tokens := strings.Split(encodedPrompt, statisticsPromptSeparator)
		if len(prompt_tokens) != 2 {
			log.Printf("[FATAL] Invalid key \"%s\" in statistics file\n", encodedPrompt)
			exit(statisticsError)
		}
		prompt := prompt{prompt_tokens[0], prompt_tokens[1]}
		stats := questionStats{data.Streak, data.Correct, data.Mistakes}
		statistics[prompt] = stats
		answers[prompt] = data.Answer
		totalProbWeight += stats.probWeight()
	}
	return statisticsDatabase{statistics, answers, totalProbWeight}
}

func (statisticsDatabase statisticsDatabase) pack() statisticsDatabaseTOML {
	statistics := map[string]promptDataTOML{}
	for prompt, stats := range statisticsDatabase.statistics {
		if stats.correct == 0 && stats.mistakes == 0 {
			continue
		}
		statistics[prompt.Encode()] = promptDataTOML{
			stats.streak,
			stats.correct,
			stats.mistakes,
			statisticsDatabase.answers[prompt],
		}
	}
	return statisticsDatabaseTOML{statistics}
}

func (m model) saveStatistics() {
	bytes, err := toml.Marshal(m.statistics.pack())
	if err != nil {
		log.Printf("[FATAL] Unachievable TOML encoding error\n")
		exit(internalError)
	}
	err = os.WriteFile("statistics.toml", bytes, 0666)
	if err != nil {
		log.Printf("[FATAL] Could not write to statistics.toml\n")
		exit(statisticsError)
	}
	log.Println("[INFO] Statistics saved")
}

func (statistics statisticsDatabase) updateStats(
	prompt prompt,
	newStats questionStats,
) {
	statistics.totalProbWeight -= statistics.statistics[prompt].probWeight()
	statistics.statistics[prompt] = newStats
	statistics.totalProbWeight += statistics.statistics[prompt].probWeight()
}

func (statistics statisticsDatabase) endStreak(prompt prompt) {
	oldStats := statistics.statistics[prompt]
	statistics.updateStats(
		prompt,
		questionStats{streak: 0, correct: oldStats.correct, mistakes: oldStats.mistakes + 1},
	)
}

func (statistics statisticsDatabase) continueStreak(prompt prompt) {
	oldStats := statistics.statistics[prompt]
	statistics.updateStats(
		prompt,
		questionStats{streak: oldStats.streak + 1, correct: oldStats.correct + 1, mistakes: oldStats.mistakes},
	)
}

func (database wordDatabase) emptyStatistics() statisticsDatabase {
	log.Printf("[INFO] Initializing statistics...\n")
	statistics := make(map[prompt]questionStats)
	answers := make(map[prompt]string)
	var totalProbWeight float32 = 0
	missing_fields_counter := 0
	for verbIndex, verb := range database.verbs {
		for clueIndex, clue := range database.formClue {
			if len(database.verbForms[verbIndex]) <= clueIndex ||
				database.verbForms[verbIndex][clueIndex] == "" {
				missing_fields_counter++
				continue
			}
			answer := database.verbForms[verbIndex][clueIndex]
			statistics[prompt{clue, verb}] = questionStats{}
			answers[prompt{clue, verb}] = answer
			totalProbWeight++
		}
	}
	if missing_fields_counter > 0 {
		log.Printf(
			"[WARNING] %d missing database fields\n",
			missing_fields_counter,
		)
	}
	log.Printf("[INFO] Finished initializing statistics\n")
	return statisticsDatabase{statistics, answers, totalProbWeight}
}

type prompt struct {
	formClue string
	Verb     string
}

type question struct {
	prompt        prompt
	correctAnswer string
}

func (statistics statisticsDatabase) getRandomQuestion() question {
	random_float_index := rand.Float32() * statistics.totalProbWeight
	for prompt, questionStats := range statistics.statistics {
		random_float_index -= questionStats.probWeight()
		if random_float_index <= 0 {
			return question{prompt, statistics.answers[prompt]}
		}
	}
	log.Print("[WARNING] Random question selection floating arithmetic problem, recalculating...")
	return statistics.getRandomQuestion()
}

type mode int

const (
	input mode = iota
	validation
)

type model struct {
	statistics *statisticsDatabase
	// Screen stuff
	mode           mode
	question       question
	inputField     textinput.Model
	wrongAnswers   uint16
	correctAnswers uint16
	streak         uint16
	// Global stuff
	isInAltscreen bool
	height        int
	width         int
}

func initialModel() model {
	database := read_database()
	statistics := database.emptyStatistics()
	question := statistics.getRandomQuestion()
	inputField := textinput.New()
	inputField.Focus()
	inputField.Prompt = ""
	inputField.Width = 15
	inputField.CharLimit = 30
	return model{
		statistics:     &statistics,
		question:       question,
		inputField:     inputField,
		isInAltscreen:  true,
		mode:           input,
		wrongAnswers:   0,
		correctAnswers: 0,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func exitNonExistingMode() {
	log.Println("[FATAL] Model is in a non-existing mode")
	exit(internalError)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			log.Println("[INFO] Quitting...")
			m.saveStatistics()
			return m, tea.Quit
		case "ctrl+a":
			return m.toggleAltScreen()
		}
	}
	switch m.mode {
	case input:
		return m.inputUpdate(msg)
	case validation:
		return m.validateUpdate(msg)
	default:
		exitNonExistingMode()
		return m, nil // unreachable
	}
}

func (m model) logMistake() {
	f, err := os.OpenFile("mistakes", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	defer f.Close()
	if err != nil {
		log.Println("[ERROR] Failed to log mistake")
		return
	}
	f.WriteString(
		fmt.Sprintf(
			"Question %s + %s:\n    Correct: %s\n    Answer: %s\n\n",
			m.question.prompt.formClue,
			m.question.prompt.Verb,
			m.question.correctAnswer,
			m.inputField.Value(),
		),
	)
	log.Println("[INFO] Logged mistake")
}

func (m model) inputUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.isAnswerCorrect() {
				m.correctAnswers++
				m.streak++
				m.statistics.continueStreak(m.question.prompt)
				log.Printf(
					"[INFO] Answer is correct, new score is %.2f\n",
					m.statistics.statistics[m.question.prompt].probWeight(),
				)
			} else {
				m.logMistake()
				m.streak = 0
				m.wrongAnswers++
				m.statistics.endStreak(m.question.prompt)
				log.Printf(
					"[INFO] Answer is wrong, new score is %.2f\n",
					m.statistics.statistics[m.question.prompt].probWeight(),
				)
			}
			m.inputField.Blur() // Removes focus
			m.mode = validation
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputField, cmd = m.inputField.Update(msg)
	return m, cmd
}

func (m model) validateUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			log.Println("[INFO] New question requested")
			m.question = m.statistics.getRandomQuestion()
			m.inputField.Reset()
			m.inputField.Focus() // Removes focus
			m.mode = input
			return m, textinput.Blink
		}
	}
	var cmd tea.Cmd
	m.inputField, cmd = m.inputField.Update(msg)
	return m, cmd
}

func (m *model) toggleAltScreen() (*model, tea.Cmd) {
	m.isInAltscreen = !m.isInAltscreen
	if m.isInAltscreen {
		return m, tea.EnterAltScreen
	} else {
		return m, tea.ExitAltScreen
	}
}

func (m model) isAnswerCorrect() bool {
	return strings.TrimSpace(m.question.correctAnswer) == strings.TrimSpace(m.inputField.Value())
}

func (m model) renderValidationRow() string {
	if m.isAnswerCorrect() {
		return correctAnswerStyle.Italic(true).Render("Correct!")
	} else {
		return wrongAnswerStyle.Render(
			italic("Wrong!") + " Correct answer is: " + bold(m.question.correctAnswer),
		)
	}
}

var (
	lightPink1    = lipgloss.ANSIColor(217)
	lightPink3    = lipgloss.ANSIColor(174)
	lightPink4    = lipgloss.ANSIColor(95)
	darkSeaGreen1 = lipgloss.ANSIColor(193)
	darkSeaGreen4 = lipgloss.ANSIColor(65)
	darkSeaGreen2 = lipgloss.ANSIColor(157)
	darkOrange    = lipgloss.ANSIColor(208)
	darkOrange3   = lipgloss.ANSIColor(166)
	salmon1       = lipgloss.ANSIColor(209)
	lightSalmon3  = lipgloss.ANSIColor(173)
	wheat4        = lipgloss.ANSIColor(101)
	// Not using ANSI here since first 16 ones could be redefined
	black = lipgloss.Color("#000000")
)

var (
	background         = lipgloss.NewStyle().Background(black)
	promptStyle        = background.Italic(true).Foreground(darkSeaGreen4)
	questionStatsStyle = background.Italic(true).Foreground(wheat4)
	questionStyle      = background.Foreground(darkSeaGreen2).Width(45 - 14)
	helpMsgStyle       = background.Foreground(lightPink4)
	helpKeyStyle       = helpMsgStyle.Bold(true)

	questionStatsAlignStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(39)
	correctAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(39).
				Foreground(darkSeaGreen2)
	wrongAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(39).
				Foreground(lightPink1)
	boxStyle = background.
			Align(lipgloss.Left, lipgloss.Center).
			PaddingTop(0).
			PaddingBottom(0).
			PaddingLeft(3).
			PaddingRight(3).
			Width(45).
			Height(9).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lightPink4).
			BorderBackground(black)
)

// I could not find a way to inline
// bold and italic tokens in lipgloss
//
// Applying style to a string that
// had its parts modified by another style
// (i.e. italic or bold styles)
// would not work correctly since
// inner styles would insert an \x1b]0m
// that would reset _all_ the styling,
// ruining the global string style
//
// That means that in lipgloss to
// have three words rendered with the same style but
// one bold, one normal and one intalic
// you would have to do
// strings.JoinSpace(
//     style.Bold(true).Render(first),
//     style.Render(second),
//     style.Italic(true).Render(third),
//)
//
// The helpers allow us to write that as
// style.Render(strings.JoinSpace(bold(first), second, italic(third)))
//
// This also allows set Width on style
// since we would no longer need to
// apply the style to each token

const csi = string('\x1b') + "["
const BoldSequence = csi + "1m"
const notBoldSequence = csi + "22m"
const ItalicSequence = csi + "3m"
const notItalicSequence = csi + "23m"

func italic(s string) string {
	return ItalicSequence + s + notItalicSequence
}

func bold(s string) string {
	return BoldSequence + s + notBoldSequence
}

type helpEntry struct {
	bindings []string
	action   string
}

var helpSeparator = helpMsgStyle.Render(" • ")

func renderHelpRow(entries []helpEntry) string {
	rendered_entries := make([]string, len(entries))
	for i, entry := range entries {
		rendered_entries[i] = helpKeyStyle.Render(strings.Join(entry.bindings, "/")) +
			helpMsgStyle.Render(" "+entry.action)
	}
	help_row := strings.Join(rendered_entries, helpSeparator)
	return lipgloss.NewStyle().Inline(true).Render(help_row)
}

func renderStatsTrisymbol(baseStyle lipgloss.Style, stats questionStats) string {
	// questionStats probably would be changed for something like visibleStats
	correctCounterStyle := baseStyle.Foreground(darkSeaGreen4)
	mistakesCounterStyle := baseStyle.Foreground(lightPink4)
	streakCounterStyle := baseStyle.Foreground(wheat4)
	return correctCounterStyle.Render(strconv.Itoa(int(stats.correct))+" ● ") +
		mistakesCounterStyle.Render(strconv.Itoa(int(stats.mistakes))+" ● ") +
		streakCounterStyle.Render(strconv.Itoa(int(stats.streak))+" ●")
}

func (m *model) renderGlobalStatsRow() string {
	current_question := int(m.correctAnswers + m.wrongAnswers)
	if m.mode == input {
		// The current one is unanswered
		current_question++
	}
	statsStyle := background.Foreground(darkSeaGreen4)
	statsTrisymbol := renderStatsTrisymbol(
		statsStyle.Bold(true),
		questionStats{m.streak, m.correctAnswers, m.wrongAnswers},
	)
	return statsStyle.Width(39-lipgloss.Width(statsTrisymbol)).AlignHorizontal(lipgloss.Left).
		Render("Question "+bold(strconv.Itoa(current_question))+".       ") +
		statsTrisymbol
}

func (m model) renderQuestion() string {
	prompt_block := lipgloss.JoinVertical(
		lipgloss.Right,
		promptStyle.Render("Form Clue: "),
		promptStyle.Render("Verb: "),
		promptStyle.Render("Verb Form: "),
	)
	question_block := lipgloss.JoinVertical(
		lipgloss.Left,
		questionStyle.Render(m.question.prompt.formClue),
		questionStyle.Render(m.question.prompt.Verb),
		questionStyle.Render(m.inputField.View()),
	)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		prompt_block,
		question_block,
	)
}

var inputHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "submit"},
	{bindings: []string{"esc", "q"}, action: "exit"},
}

func (m model) renderQuestionStatsRow() string {
	return questionStatsAlignStyle.Render(questionStatsStyle.Render("[question stats: ") +
		renderStatsTrisymbol(background.Italic(true), m.statistics.statistics[m.question.prompt]) +
		questionStatsStyle.Render("]"))
}

func (m model) inputView() string {
	return boxStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderGlobalStatsRow(),
		"",
		m.renderQuestion(),
		"",
		m.renderQuestionStatsRow(),
		"",
		renderHelpRow(inputHelp[:]),
	))
}

var validationHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "continue"},
	{bindings: []string{"esc", "q"}, action: "exit"},
}

func (m model) validationView() string {
	return boxStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderGlobalStatsRow(),
		"",
		m.renderQuestion(),
		"",
		m.renderValidationRow(),
		"",
		renderHelpRow(validationHelp[:]),
	))
}

func (m model) View() string {
	var content string
	switch m.mode {
	case input:
		content = m.inputView()
	case validation:
		content = m.validationView()
	default:
		exitNonExistingMode()
	}
	if !m.isInAltscreen {
		return content + "\n"
	}
	return lipgloss.NewStyle().
		Align(lipgloss.Center, lipgloss.Center).
		Width(m.width).
		Height(m.height).
		Background(black).
		Render(content)
}

func main() {
	f, err := tea.LogToFile("log", "")
	defer f.Close()
	if err != nil {
		log.Printf("[FATAL] %v\n", err)
		exit(loggingError)
	}

	log.Println("[INFO] Starting app...")
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	log.Println("[INFO] Starting UI loop...")
	if _, err := p.Run(); err != nil {
		log.Printf("[FATAL] Program finished with error:\n%v", err)
		exit(teaError)
	}
	log.Println("[INFO] Finished successfully")
}
