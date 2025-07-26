package survey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skip2/go-qrcode"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxStringLen  = 100
	surveyTimeout = time.Hour
)

type Option struct {
	Title string
	Votes int
}

type Options []Option

type OptionResult struct {
	Title   string
	votes   int
	percent float64
}

func (o OptionResult) Percent() string {
	if o.votes < 0 {
		return "-"
	}
	return strconv.FormatFloat(o.percent, 'f', 1, 64)
}

func (o OptionResult) Votes() string {
	if o.votes < 0 {
		return "-"
	}
	return strconv.Itoa(o.votes)
}

func (o OptionResult) String() string {
	return fmt.Sprintf("%s: %d (%.1f%%)", o.Title, o.votes, o.percent)
}

func (o Options) result(sum int, hidden bool) []OptionResult {
	if sum <= 0 {
		sum = 1
	}

	var res []OptionResult
	if hidden {
		for _, option := range o {
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   -1,
				percent: 0,
			})
		}
	} else {
		for _, option := range o {
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   option.Votes,
				percent: float64(option.Votes) / float64(sum) * 100,
			})
		}
	}
	return res
}

type SurveyID string
type UserID string

type Survey struct {
	mutex        sync.Mutex
	definition   SurveyDef
	surveyID     SurveyID
	userID       UserID
	qrCode       string
	options      Options
	number       int
	votesCounted map[UserID]struct{}
	resultHidden bool
	creationTime time.Time
}

func (s *Survey) Lock() {
	s.mutex.Lock()
}

func (s *Survey) Unlock() {
	s.mutex.Unlock()
}

type Result struct {
	Title  string
	QRCode string
	Votes  int
	Result []OptionResult
}

func (s *Survey) Result() Result {
	return Result{
		Title:  s.definition.Title,
		QRCode: s.qrCode,
		Votes:  len(s.votesCounted),
		Result: s.options.result(len(s.votesCounted), s.resultHidden),
	}
}

type Question struct {
	Number     int
	SurveyID   SurveyID
	Definition SurveyDef
}

func (s *Survey) Question() Question {
	return Question{
		Number:     s.number,
		SurveyID:   s.surveyID,
		Definition: s.definition,
	}
}

var (
	mutex   sync.Mutex
	surveys = map[SurveyID]*Survey{}
)

type SurveyDef struct {
	Title    string
	Options  []string
	Multiple bool
}

func (d SurveyDef) Valid() bool {
	return d.Title != "" && len(d.Options) >= 2
}

func (d SurveyDef) String() string {
	str := d.clean(d.Title)
	if d.Multiple {
		str += ";m"
	} else {
		str += ";s"
	}
	for _, o := range d.Options {
		if o != "" {
			str += ";" + d.clean(o)
		}
	}
	return str
}

func (d SurveyDef) clean(o string) string {
	o = strings.TrimSpace(o)
	o = strings.ReplaceAll(o, ";", "")
	return o
}

func DefinitionFromString(str string) (SurveyDef, error) {
	parts := strings.Split(str, ";")
	if len(parts) < 4 {
		return SurveyDef{}, errors.New("Ungültige Umfrage-Definition!")
	}

	def := SurveyDef{
		Title:    parts[0],
		Multiple: parts[1] == "m",
	}

	for _, option := range parts[2:] {
		option = strings.TrimSpace(option)
		if option != "" {
			def.Options = append(def.Options, option)
		}
	}

	if !def.Valid() {
		return SurveyDef{}, errors.New("Ungültige Umfrage-Definition!")
	}

	return def, nil
}

func New(host string, userid UserID, surveyId SurveyID, def SurveyDef) error {
	opt := make([]Option, len(def.Options))
	for i, option := range def.Options {
		option = strings.TrimSpace(option)
		if option == "" {
			return fmt.Errorf("Option %d ist leer!", i+1)
		} else if len(option) > maxStringLen {
			return fmt.Errorf("Option %d ist zu lang! Maximal %d Zeichen erlaubt.", i+1, maxStringLen)
		}
		opt[i] = Option{Title: option, Votes: 0}
	}

	url := host + "/vote?id=" + string(surveyId)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return fmt.Errorf("could not create qr code: %w", err)
	}

	def.Title = strings.TrimSpace(def.Title)
	if def.Title == "" {
		return errors.New("Es fehlt der Titel!")
	} else if len(def.Title) > maxStringLen {
		return fmt.Errorf("Der Titel ist zu lang! Maximal %d Zeichen erlaubt.", maxStringLen)
	}

	if len(opt) < 2 {
		return errors.New("Es müssen mindestens zwei Optionen angegeben werden!")
	}

	mutex.Lock()
	defer mutex.Unlock()

	num := 0
	if existingSurvey, exists := surveys[surveyId]; exists {
		num = existingSurvey.number + 1
		if existingSurvey.userID != userid {
			return errors.New("Diese Umfrage existiert bereits und wurde von einem anderen Benutzer erstellt!")
		}
	}

	survey := Survey{
		surveyID:     surveyId,
		userID:       userid,
		qrCode:       base64.StdEncoding.EncodeToString(qrCode),
		options:      opt,
		definition:   def,
		number:       num,
		resultHidden: true,
		votesCounted: make(map[UserID]struct{}),
		creationTime: time.Now(),
	}
	surveys[surveyId] = &survey

	log.Printf("created a survey with %d options, in total %d", len(opt), len(surveys))

	return nil
}

func getSurveyToVote(surveyID SurveyID) (*Survey, bool) {
	mutex.Lock()
	defer mutex.Unlock()
	survey, exists := surveys[surveyID]
	return survey, exists
}

func getSurveyCheckUser(userId UserID, surveyID SurveyID) (*Survey, bool) {
	mutex.Lock()
	defer mutex.Unlock()
	survey, exists := surveys[surveyID]
	if !exists {
		return nil, false
	}
	if survey.userID != userId {
		return nil, false
	}
	return survey, exists
}

func Uncover(userid UserID, surveyID SurveyID, debug bool) error {
	survey, exists := getSurveyCheckUser(userid, surveyID)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.userID != userid {
		return errors.New("Sie sind nicht der Ersteller dieser Umfrage!")
	}

	votes := len(survey.votesCounted)
	if !debug && votes > 0 && votes <= 2 {
		return errors.New("Es sind noch nicht genug Stimmen abgegeben worden!")
	}

	survey.resultHidden = false
	return nil
}

func GetResult(userId UserID, surveyID SurveyID) Result {
	survey, exists := getSurveyCheckUser(userId, surveyID)
	if !exists {
		return Result{Title: "Die Umfrage existiert nicht!"}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Result()
}

func GetRunningSurvey(userID UserID, surveyID SurveyID) (SurveyDef, bool) {
	survey, exists := getSurveyCheckUser(userID, surveyID)
	if !exists {
		return SurveyDef{}, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.definition, true
}

func IsHiddenRunning(userID UserID, surveyID SurveyID) (bool, bool) {
	survey, exists := getSurveyCheckUser(userID, surveyID)
	if !exists {
		return false, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.resultHidden, true
}

func Vote(surveyID SurveyID, voterId UserID, option []int, number int) error {
	survey, exists := getSurveyToVote(surveyID)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

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

func HasVoted(surveyID SurveyID, voterId UserID) bool {
	survey, exists := getSurveyToVote(surveyID)
	if !exists {
		return false
	}

	survey.Lock()
	defer survey.Unlock()

	_, voted := survey.votesCounted[voterId]
	return voted
}

func GetQuestion(surveyID SurveyID) Question {
	survey, exists := getSurveyToVote(surveyID)
	if !exists {
		return Question{Definition: SurveyDef{Title: "Die Umfrage existiert nicht!"}}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Question()
}

func StartSurveyCheck() {
	go func() {
		log.Println("Starting survey cleanup routine, timeout", surveyTimeout)
		for {
			time.Sleep(surveyTimeout / 2)
			deleted, remaining := cleanup()
			if deleted > 0 || remaining > 0 {
				log.Printf("Deleted %d old surveys, %d surveys remaining\n", deleted, remaining)
			}
		}
	}()
}

func cleanup() (int, int) {
	mutex.Lock()
	defer mutex.Unlock()

	deleteCount := 0
	for id, survey := range surveys {
		if time.Since(survey.creationTime) > surveyTimeout {
			delete(surveys, id)
			deleteCount++
		}
	}

	return deleteCount, len(surveys)
}
