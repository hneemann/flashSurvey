package survey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skip2/go-qrcode"
	"strconv"
	"strings"
	"sync"
)

type Option struct {
	Title string
	Votes int
}

type Options []Option

type OptionResult struct {
	Title   string
	Votes   int
	percent float64
}

func (o OptionResult) Percent() string {
	return strconv.FormatFloat(o.percent, 'f', 1, 64)
}

func (o OptionResult) String() string {
	return fmt.Sprintf("%s: %d (%.1f%%)", o.Title, o.Votes, o.percent)
}

func (o Options) result(sum int) []OptionResult {
	if sum == 0 {
		sum = 1 // avoid division by zero
	}
	var res []OptionResult
	for _, option := range o {
		res = append(res, OptionResult{
			Title:   option.Title,
			Votes:   option.Votes,
			percent: float64(option.Votes) / float64(sum) * 100,
		})
	}
	return res
}

type SurveyID string
type VoterID string

type Survey struct {
	surveyID      SurveyID
	qrCode        string
	options       Options
	title         string
	number        int
	multiple      bool
	votesCounted  map[VoterID]struct{}
	optionStrings []string
}

type Result struct {
	Title  string
	QRCode string
	Result []OptionResult
}

func (s Survey) Result() Result {
	return Result{
		Title:  s.title,
		QRCode: s.qrCode,
		Result: s.options.result(len(s.votesCounted)),
	}
}

type Question struct {
	Title    string
	Number   int
	SurveyID SurveyID
	Options  []string
	Multiple bool
}

func (s Survey) Question() Question {
	return Question{
		Title:    s.title,
		Number:   s.number,
		SurveyID: s.surveyID,
		Multiple: s.multiple,
		Options:  s.optionStrings,
	}
}

var (
	mutex   sync.Mutex
	surveys = map[SurveyID]Survey{}
)

func New(host string, id SurveyID, title string, multiple bool, options ...string) error {
	opt := make([]Option, len(options))
	for i, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			return errors.New("Option " + strconv.Itoa(i+1) + " ist leer!")
		}
		opt[i] = Option{Title: option, Votes: 0}
	}

	url := host + "/vote?id=" + string(id)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return fmt.Errorf("could not create qr code: %w", err)
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("Es fehlt der Titel!")
	}

	if len(opt) < 2 {
		return errors.New("Es müssen mindestens zwei Optionen angegeben werden!")
	}

	if multiple && len(opt) < 3 {
		return errors.New("Bei Mehrfachauswahl müssen mindestens drei Optionen angegeben werden!")
	}

	mutex.Lock()
	defer mutex.Unlock()

	num := 0
	if existingSurvey, exists := surveys[id]; exists {
		num = existingSurvey.number + 1
	}

	surveys[id] = Survey{
		surveyID:      id,
		qrCode:        base64.StdEncoding.EncodeToString(qrCode),
		options:       opt,
		optionStrings: options,
		number:        num,
		multiple:      multiple,
		title:         title,
		votesCounted:  make(map[VoterID]struct{}),
	}

	return nil
}

func Vote(surveyID SurveyID, voterId VoterID, option []int, number int) error {
	mutex.Lock()
	defer mutex.Unlock()
	survey, exists := surveys[surveyID]
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}
	if number != survey.number {
		return errors.New("Diese Wahl war schon abgeschlossen!")
	}

	if _, voted := survey.votesCounted[voterId]; voted {
		return errors.New("Sie haben bereits abgestimmt!")
	}

	for _, opt := range option {
		if opt < 0 || opt >= len(survey.options) {
			return errors.New("Ungültige Option!")
		}
		survey.options[opt].Votes++
	}

	survey.votesCounted[voterId] = struct{}{}

	return nil
}

func GetResult(surveyID SurveyID) Result {
	mutex.Lock()
	defer mutex.Unlock()
	survey, exists := surveys[surveyID]
	if !exists {
		return Result{Title: "Die Umfrage existiert nicht!"}
	}
	return survey.Result()
}

func GetQuestion(surveyID SurveyID) Question {
	mutex.Lock()
	defer mutex.Unlock()
	survey, exists := surveys[surveyID]
	if !exists {
		return Question{Title: "Die Umfrage existiert nicht!"}
	}
	return survey.Question()
}
