package main

import (
	"errors"
	"fmt"
	"io/fs"
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

const (
	wordDatabasePath = "words.xlsx"
	logPath          = "log"
	mistakesPath     = "mistakes"
	statisticsPath   = "statistics.toml"
)

type wordDatabase struct {
	formClue  []string
	verbs     []string
	verbForms [][]string
}

func read_database() wordDatabase {
	table, err := excelize.OpenFile(wordDatabasePath)
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
	// These are fields present in file
	// yet not existing in word database
	deadRecords map[string]promptDataTOML
}

const statisticsPromptSeparator = "+"

func (statistics statisticsDatabase) sortPromptsArbitraryOrder() []prompt {
	orderedPromptList := make([]prompt, len(statistics.statistics))
	i := 0
	for prompt := range statistics.statistics {
		orderedPromptList[i] = prompt
		i++
	}
	return orderedPromptList
}

func (prompt prompt) encode() string {
	return fmt.Sprintf("%s%s%s", prompt.formClue, statisticsPromptSeparator, prompt.verb)
}

func decodePrompt(encodedPrompt string) prompt {
	prompt_tokens := strings.Split(encodedPrompt, statisticsPromptSeparator)
	if len(prompt_tokens) != 2 {
		log.Printf("[FATAL] Invalid key \"%s\" in statistics file\n", encodedPrompt)
		exit(statisticsError)
	}
	return prompt{prompt_tokens[0], prompt_tokens[1]}
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

func (statistics statisticsDatabase) expand(statisticsTOML statisticsDatabaseTOML) {
	log.Println("[INFO] Updating statistics with content from file...")
	resetRecordsCount := 0
	for encodedPrompt, data := range statisticsTOML.Statistics {
		prompt := decodePrompt(encodedPrompt)
		_, exists := statistics.statistics[prompt]
		if !exists {
			statistics.deadRecords[encodedPrompt] = data
			continue
		}
		if data.Answer != statistics.answers[prompt] {
			resetRecordsCount++
			continue
		}
		stats := questionStats{data.Streak, data.Correct, data.Mistakes}
		statistics.updateStats(prompt, stats)
	}
	if len(statistics.deadRecords) > 0 {
		log.Printf(
			"[INFO] %d questions no longer exist, ignoring statistics for them\n",
			len(statistics.deadRecords),
		)
	}
	if resetRecordsCount > 0 {
		log.Printf(
			"[WARNING] %d questions have their answer changed, resetting statistics for them\n",
			resetRecordsCount,
		)
	}
}

func (statisticsDatabase statisticsDatabase) pack() statisticsDatabaseTOML {
	statistics := statisticsDatabase.deadRecords
	for prompt, stats := range statisticsDatabase.statistics {
		if stats.correct == 0 && stats.mistakes == 0 {
			continue
		}
		statistics[prompt.encode()] = promptDataTOML{
			stats.streak,
			stats.correct,
			stats.mistakes,
			statisticsDatabase.answers[prompt],
		}
	}
	return statisticsDatabaseTOML{statistics}
}

func (screen quizScreen) saveStatistics() {
	bytes, err := toml.Marshal(screen.statistics.pack())
	if err != nil {
		log.Printf("[FATAL] Unachievable TOML encoding error\n")
		exit(internalError)
	}
	err = os.WriteFile(statisticsPath, bytes, 0666)
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
	return statisticsDatabase{statistics, answers, totalProbWeight, map[string]promptDataTOML{}}
}

func (database wordDatabase) loadStatistics() statisticsDatabase {
	statistics := database.emptyStatistics()
	log.Printf("[INFO] Trying to read statistics file...")
	bytes, err := os.ReadFile(statisticsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Println("[INFO] Statistics file not found")
		} else {
			log.Println("[ERROR] Failed to read statistics file")
		}
	} else {
		var statisticsTOML statisticsDatabaseTOML
		err = toml.Unmarshal(bytes, &statisticsTOML)
		if err != nil {
			log.Printf("[FATAL] Failed to parse TOML statistics file:\n %s \n", err)
			exit(statisticsError)
		}
		statistics.expand(statisticsTOML)
	}
	return statistics
}

type prompt struct {
	formClue string
	verb     string
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
	screen        tea.Model
	isInAltscreen bool
	height        int
	width         int
}

type quizScreen struct {
	statistics     *statisticsDatabase
	mode           mode
	question       question
	inputField     textinput.Model
	wrongAnswers   uint16
	correctAnswers uint16
	streak         uint16
}

type statisticsScreen struct {
	previousScreen    *quizScreen
	statistics        *statisticsDatabase
	orderedPromptList []prompt
	firstShownIndex   int
	selectedRow       int
}

func initialModel() model {
	database := read_database()
	statistics := database.loadStatistics()
	question := statistics.getRandomQuestion()
	inputField := textinput.New()
	inputField.Focus()
	inputField.Prompt = ""
	inputField.Width = 15
	inputField.CharLimit = 30
	return model{
		screen: quizScreen{
			statistics:     &statistics,
			question:       question,
			inputField:     inputField,
			mode:           input,
			wrongAnswers:   0,
			correctAnswers: 0,
		},
		isInAltscreen: true,
	}
}

func (screen quizScreen) Init() tea.Cmd {
	return textinput.Blink
}

func (screen statisticsScreen) Init() tea.Cmd {
	return nil
}

func (m model) Init() tea.Cmd {
	return m.screen.Init()
}

func exitNonExistingMode() {
	log.Println("[FATAL] Screen is in a non-existing mode")
	exit(internalError)
}

type ExitScreenMessage struct{}
type ScreenExitedMessage struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			log.Println("[INFO] Quitting...")
			return m, func() tea.Msg { return ExitScreenMessage{} }
		case "ctrl+a":
			return m.toggleAltScreen()
		}
	case ScreenExitedMessage:
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.screen, cmd = m.screen.Update(msg)
	return m, cmd
}

func (screen quizScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s":
			return statisticsScreen{
				previousScreen:    &screen,
				statistics:        screen.statistics,
				orderedPromptList: screen.statistics.sortPromptsArbitraryOrder(),
				firstShownIndex:   0,
				selectedRow:       0,
			}, nil
		}
	case ExitScreenMessage:
		screen.saveStatistics()
		return screen, func() tea.Msg { return ScreenExitedMessage{} }
	}
	switch screen.mode {
	case input:
		return screen.inputUpdate(msg)
	case validation:
		return screen.validateUpdate(msg)
	default:
		exitNonExistingMode()
		return screen, nil // unreachable
	}
}

func (screen statisticsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ExitScreenMessage:
		return screen, func() tea.Msg { return ScreenExitedMessage{} }
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s", "backspace":
			return screen.previousScreen, nil
		case "j", "down":
			screen.scrollDown()
			return screen, nil
		case "k", "up":
			screen.scrollUp()
			return screen, nil
		}
	}
	return screen, nil
}

func (screen quizScreen) logMistake() {
	f, err := os.OpenFile(mistakesPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	defer f.Close()
	if err != nil {
		log.Println("[ERROR] Failed to log mistake")
		return
	}
	f.WriteString(
		fmt.Sprintf(
			"Question %s + %s:\n    Correct: %s\n    Answer: %s\n\n",
			screen.question.prompt.formClue,
			screen.question.prompt.verb,
			screen.question.correctAnswer,
			screen.inputField.Value(),
		),
	)
	log.Println("[INFO] Logged mistake")
}

func (screen quizScreen) inputUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if screen.isAnswerCorrect() {
				screen.correctAnswers++
				screen.streak++
				screen.statistics.continueStreak(screen.question.prompt)
				log.Printf(
					"[INFO] Answer is correct, new score is %.2f\n",
					screen.statistics.statistics[screen.question.prompt].probWeight(),
				)
			} else {
				screen.logMistake()
				screen.streak = 0
				screen.wrongAnswers++
				screen.statistics.endStreak(screen.question.prompt)
				log.Printf(
					"[INFO] Answer is wrong, new score is %.2f\n",
					screen.statistics.statistics[screen.question.prompt].probWeight(),
				)
			}
			screen.inputField.Blur() // Removes focus
			screen.mode = validation
			return screen, nil
		}
	}
	var cmd tea.Cmd
	screen.inputField, cmd = screen.inputField.Update(msg)
	return screen, cmd
}

func (screen quizScreen) validateUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			log.Println("[INFO] New question requested")
			screen.question = screen.statistics.getRandomQuestion()
			screen.inputField.Reset()
			screen.inputField.Focus() // Removes focus
			screen.mode = input
			return screen, textinput.Blink
		}
	}
	var cmd tea.Cmd
	screen.inputField, cmd = screen.inputField.Update(msg)
	return screen, cmd
}

func (m *model) toggleAltScreen() (*model, tea.Cmd) {
	m.isInAltscreen = !m.isInAltscreen
	if m.isInAltscreen {
		return m, tea.EnterAltScreen
	} else {
		return m, tea.ExitAltScreen
	}
}

func (screen quizScreen) isAnswerCorrect() bool {
	return strings.TrimSpace(screen.question.correctAnswer) == strings.TrimSpace(screen.inputField.Value())
}

func (screen quizScreen) renderValidationRow() string {
	if screen.isAnswerCorrect() {
		return correctAnswerStyle.Italic(true).Render("Correct!")
	} else {
		return wrongAnswerStyle.Render(
			italic("Wrong!") + " Correct answer is: " + bold(screen.question.correctAnswer),
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

const (
	boxWidth          = 45
	boxHeight         = 12
	horizontalPadding = 3
	verticalPadding   = 1
	totalBoxWidth     = boxWidth + 2*horizontalPadding
	totalBoxHeight    = boxHeight + 2*verticalPadding
)

var (
	background            = lipgloss.NewStyle().Background(black)
	promptStyle           = background.Italic(true).Foreground(darkSeaGreen4)
	promptStatsEntryStyle = background.Italic(false).Foreground(darkSeaGreen4)
	questionStatsStyle    = background.Italic(true).Foreground(wheat4)
	questionStyle         = background.Foreground(darkSeaGreen2)
	statsTitleStyle       = background.Foreground(darkSeaGreen4).Width(boxWidth)
	helpMsgStyle          = background.Foreground(lightPink4)
	helpKeyStyle          = helpMsgStyle.Bold(true)

	questionStatsAlignStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(boxWidth)
	correctAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(boxWidth).
				Foreground(darkSeaGreen2)
	wrongAnswerStyle = background.
				AlignHorizontal(lipgloss.Center).
				Width(boxWidth).
				Foreground(lightPink1)
	boxStyle = background.
			Align(lipgloss.Left, lipgloss.Center).
			PaddingTop(0).
			PaddingBottom(0).
			PaddingLeft(horizontalPadding).
			PaddingTop(verticalPadding).
			PaddingBottom(verticalPadding).
			Width(totalBoxWidth).
			Height(totalBoxHeight).
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

func (screen quizScreen) renderGlobalStatsRow() string {
	current_question := int(screen.correctAnswers + screen.wrongAnswers)
	if screen.mode == input {
		// The current one is unanswered
		current_question++
	}
	statsStyle := background.Foreground(darkSeaGreen4)
	statsTrisymbol := renderStatsTrisymbol(
		statsStyle.Bold(true),
		questionStats{screen.streak, screen.correctAnswers, screen.wrongAnswers},
	)
	return statsStyle.Width(boxWidth-lipgloss.Width(statsTrisymbol)).AlignHorizontal(lipgloss.Left).
		Render("Question "+bold(strconv.Itoa(current_question))+".       ") +
		statsTrisymbol
}

func (screen quizScreen) renderQuestion() string {
	prompts := []string{
		promptStyle.Render("Form Clue: "),
		promptStyle.Render("Verb: "),
		promptStyle.Render("Verb Form: "),
	}
	prompt_block := lipgloss.JoinVertical(
		lipgloss.Right,
		prompts...,
	)
	maxlen := 0
	for _, prompt := range prompts {
		maxlen = max(maxlen, lipgloss.Width(prompt))
	}
	questionBlockWidth := boxWidth - maxlen
	questionBoxStyle := questionStyle.Width(questionBlockWidth)
	question_block := lipgloss.JoinVertical(
		lipgloss.Left,
		questionBoxStyle.Render(screen.question.prompt.formClue),
		questionBoxStyle.Render(screen.question.prompt.verb),
		questionBoxStyle.Render(screen.inputField.View()),
	)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		prompt_block,
		question_block,
	)
}

var inputHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "submit"},
	{bindings: []string{"ctrl+s"}, action: "stats"},
	{bindings: []string{"esc"}, action: "exit"},
}

func (screen quizScreen) renderQuestionStatsRow() string {
	return questionStatsAlignStyle.Render(questionStatsStyle.Render("[question stats: ") +
		renderStatsTrisymbol(background.Italic(true), screen.statistics.statistics[screen.question.prompt]) +
		questionStatsStyle.Render("]"))
}

func (screen quizScreen) inputView() string {
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		screen.renderGlobalStatsRow(),
		"",
		screen.renderQuestion(),
		"",
		"",
		screen.renderQuestionStatsRow(),
	)
	footer := renderHelpRow(inputHelp[:])
	spacing := boxHeight - lipgloss.Height(body) - lipgloss.Height(footer)
	content := body + strings.Repeat("\n", spacing+1) + footer
	return boxStyle.Render(content)
}

var validationHelp = [...]helpEntry{
	{bindings: []string{"enter"}, action: "next"},
	{bindings: []string{"ctrl+s"}, action: "stats"},
	{bindings: []string{"esc"}, action: "exit"},
}

func (screen quizScreen) validationView() string {
	footer := renderHelpRow(validationHelp[:])
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		screen.renderGlobalStatsRow(),
		"",
		screen.renderQuestion(),
		"",
		"",
		screen.renderValidationRow(),
	)
	spacing := boxHeight - lipgloss.Height(body) - lipgloss.Height(footer)
	content := body + strings.Repeat("\n", spacing+1) + footer
	return boxStyle.Render(content)
}

func (screen statisticsScreen) renderStatEntry(prompt prompt, selected bool) string {
	statsTrisymbol := renderStatsTrisymbol(
		background.Bold(selected).Italic(selected),
		screen.statistics.statistics[prompt],
	)
	if selected {
		bracketStyle := background.Italic(true).Foreground(wheat4)
		statsTrisymbol = bracketStyle.Render("[") + statsTrisymbol + bracketStyle.Render("]")
	} else {
		statsTrisymbol += background.Render(" ")
	}
	promptFormated := fmt.Sprintf("%s + %s", prompt.formClue, prompt.verb)
	if selected {
		promptFormated = "> " + promptFormated
	}
	return promptStatsEntryStyle.
		Bold(selected).
		Italic(selected).
		Width(boxWidth-lipgloss.Width(statsTrisymbol)).
		AlignHorizontal(lipgloss.Left).
		Render(promptFormated) +
		statsTrisymbol
}

var statisticsScreenHelp = [...]helpEntry{
	{bindings: []string{"k", "↑"}, action: "up"},
	{bindings: []string{"j", "↓"}, action: "down"},
	{bindings: []string{"backspace"}, action: "back"},
	{bindings: []string{"esc"}, action: "exit"},
}

func (screen *statisticsScreen) scrollDown() {
	keepOnScreen := 2
	shownRows := boxHeight - 2 - 2
	if screen.selectedRow < shownRows-keepOnScreen-1 {
		screen.selectedRow++
		return
	}
	if screen.firstShownIndex+shownRows < len(screen.orderedPromptList) {
		screen.firstShownIndex++
	} else if screen.selectedRow < shownRows-1 {
		screen.selectedRow++
	}
}

func (screen *statisticsScreen) scrollUp() {
	keepOnScreen := 2
	if screen.selectedRow > keepOnScreen {
		screen.selectedRow--
		return
	}
	if screen.firstShownIndex > 0 {
		screen.firstShownIndex--
	} else if screen.selectedRow > 0 {
		screen.selectedRow--
	}
}

func (screen statisticsScreen) View() string {
	footer := renderHelpRow(statisticsScreenHelp[:])
	renderedLines := []string{statsTitleStyle.Render("Statistics"), ""}
	shownRows := boxHeight - 2 - 2
	for row := 0; row < shownRows; row++ {
		promptIndex := screen.firstShownIndex + row
		if promptIndex >= len(screen.orderedPromptList) {
			break
		}
		entryPrompt := screen.orderedPromptList[promptIndex]
		renderedLines = append(renderedLines, screen.renderStatEntry(
			entryPrompt,
			row == screen.selectedRow,
		))
	}
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		renderedLines...,
	)
	spacing := boxHeight - lipgloss.Height(body) - lipgloss.Height(footer)
	content := body + strings.Repeat("\n", spacing+1) + footer
	return boxStyle.Render(content)
}

func (screen quizScreen) View() string {
	switch screen.mode {
	case input:
		return screen.inputView()
	case validation:
		return screen.validationView()
	}
	exitNonExistingMode()
	return "" //unreachable
}

func (m model) View() string {
	content := m.screen.View()
	if !m.isInAltscreen {
		// Terminal wants everything to end
		// with explicit newline character
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
	f, err := tea.LogToFile(logPath, "")
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
