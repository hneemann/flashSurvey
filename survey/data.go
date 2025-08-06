package survey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skip2/go-qrcode"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxStringLen = 100
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

func (o OptionResult) PercentVal(max float64) float64 {
	if o.votes < 0 {
		return 0
	}
	return o.percent / max * 100
}

func (o OptionResult) PercentValRemain(max float64) float64 {
	if o.votes < 0 {
		return 100
	}
	return 100 - o.percent/max*100
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

func (o Options) result(sum int, hidden bool) ([]OptionResult, float64) {
	if sum <= 0 {
		sum = 1
	}

	var maxPercent float64

	var res []OptionResult
	if hidden {
		for _, option := range o {
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   -1,
				percent: 0,
			})
			maxPercent = 100.0
		}
	} else {
		for _, option := range o {
			percent := float64(option.Votes) / float64(sum) * 100
			res = append(res, OptionResult{
				Title:   option.Title,
				votes:   option.Votes,
				percent: percent,
			})
			if percent > maxPercent {
				maxPercent = percent
			}
		}
		if maxPercent < 1 {
			maxPercent = 1.0
		}
	}
	return res, maxPercent
}

type SurveyId string
type UserId string

type Survey struct {
	mutex    sync.Mutex
	question SurveyQuestion
	surveyId SurveyId
	userId   UserId
	qrCode   string
	options  Options
	// The number is the number of times the survey has been updated.
	// This is incremented whenever the question or options are changed.
	// It is not incremented for votes.
	number       int
	votesCounted map[UserId]struct{}
	resultHidden bool
	creationTime time.Time
	// The version is incremented whenever the survey is changed.
	// This includes votes.
	version       int
	changedNotify chan struct{}
}

func NewSurvey(userId UserId, def SurveyQuestion, opt []Option, host string) (*Survey, error) {
	surveyId := SurveyId(RandomString())

	url := host + "/vote/?id=" + string(surveyId)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return nil, fmt.Errorf("could not create qr code: %w", err)
	}

	return &Survey{
		question:      def,
		surveyId:      surveyId,
		qrCode:        base64.StdEncoding.EncodeToString(qrCode),
		userId:        userId,
		options:       opt,
		number:        1,
		votesCounted:  make(map[UserId]struct{}),
		resultHidden:  true,
		creationTime:  time.Now(),
		version:       1,
		changedNotify: make(chan struct{}),
	}, nil
}

func (s *Survey) Lock() {
	s.mutex.Lock()
}

func (s *Survey) Unlock() {
	s.mutex.Unlock()
}

func (s *Survey) changed() {
	s.version++
	if s.changedNotify != nil {
		close(s.changedNotify)
	}
	s.changedNotify = make(chan struct{})
}

func (s *Survey) Update(def SurveyQuestion, opt []Option) {
	s.Lock()
	defer s.Unlock()
	s.question = def
	s.options = opt
	s.number++
	s.votesCounted = make(map[UserId]struct{})
	s.resultHidden = true
	s.creationTime = time.Now()
	s.changed()
}

type Result struct {
	Title      string
	QRCode     string
	Votes      int
	Result     []OptionResult
	MaxPercent float64
	Version    int
}

func (s *Survey) Result() Result {
	result, maxPercent := s.options.result(len(s.votesCounted), s.resultHidden)
	return Result{
		Title:      s.question.Title,
		QRCode:     s.qrCode,
		Votes:      len(s.votesCounted),
		MaxPercent: maxPercent,
		Result:     result,
		Version:    s.version,
	}
}

type Question struct {
	Number   int
	SurveyId SurveyId
	Question SurveyQuestion
}

func (s *Survey) Question() Question {
	return Question{
		Number:   s.number,
		SurveyId: s.surveyId,
		Question: s.question,
	}
}

type Surveys struct {
	mutex               sync.RWMutex
	surveys             map[SurveyId]*Survey
	host                string
	debug               bool
	voteIfResultVisible bool
}

var closedChannel chan struct{}

func init() {
	closedChannel = make(chan struct{})
	close(closedChannel)
}

func New(host string, timeoutMin int, voteIfResultVisible, debug bool) *Surveys {
	s := &Surveys{
		surveys:             make(map[SurveyId]*Survey),
		host:                host,
		voteIfResultVisible: voteIfResultVisible,
		debug:               debug,
	}
	s.startSurveyTimeoutCheck(timeoutMin)
	return s
}

type SurveyQuestion struct {
	Title    string
	Options  []string
	Multiple bool
}

func (d SurveyQuestion) Valid() bool {
	return d.Title != "" && len(d.Options) >= 2
}

func (d SurveyQuestion) String() string {
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

func (d SurveyQuestion) clean(o string) string {
	o = strings.TrimSpace(o)
	o = strings.ReplaceAll(o, ";", "")
	return o
}

func DefinitionFromString(str string) (SurveyQuestion, error) {
	parts := strings.Split(str, ";")
	if len(parts) < 4 {
		return SurveyQuestion{}, errors.New("Ungültige Umfrage-Definition!")
	}

	def := SurveyQuestion{
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
		return SurveyQuestion{}, errors.New("Ungültige Umfrage-Definition!")
	}

	return def, nil
}

func (s *Surveys) New(userId UserId, knownSurveyId SurveyId, def SurveyQuestion) (SurveyId, error) {
	opt := make([]Option, len(def.Options))
	for i, option := range def.Options {
		option = strings.TrimSpace(option)
		if option == "" {
			return "", fmt.Errorf("Option %d ist leer!", i+1)
		} else if len(option) > maxStringLen {
			return "", fmt.Errorf("Option %d ist zu lang! Maximal %d Zeichen erlaubt.", i+1, maxStringLen)
		}
		opt[i] = Option{Title: option, Votes: 0}
	}

	def.Title = strings.TrimSpace(def.Title)
	if def.Title == "" {
		return "", errors.New("Es fehlt der Titel!")
	} else if len(def.Title) > maxStringLen {
		return "", fmt.Errorf("Der Titel ist zu lang! Maximal %d Zeichen erlaubt.", maxStringLen)
	}

	if len(opt) < 2 {
		return "", errors.New("Es müssen mindestens zwei Optionen angegeben werden!")
	}

	if len(knownSurveyId) == IdLength {
		ok, err := s.tryUpdate(userId, knownSurveyId, def, opt)
		if err != nil {
			return "", err
		}
		if ok { // There was an existing survey to update
			log.Printf("updated survey with %d options, in total %d surveys", len(opt), s.getSurveyCount())
			return knownSurveyId, nil
		}
	}

	su, err := NewSurvey(userId, def, opt, s.host)
	if err != nil {
		return "", err
	}
	log.Printf("created survey with %d options, in total %d surveys", len(opt), s.getSurveyCount()+1)

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.surveys[su.surveyId]; exists {
		// Almost impossible, but just in case
		log.Printf("survey with ID %s already exists, this should not happen!", su.surveyId)
		return "", fmt.Errorf("Umfrage mit ID %s existiert bereits!", su.surveyId)
	}

	s.surveys[su.surveyId] = su

	return su.surveyId, nil
}

func (s *Surveys) getSurveyCount() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return len(s.surveys)
}

func (s *Surveys) tryUpdate(userId UserId, oldSurveyId SurveyId, def SurveyQuestion, opt []Option) (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if existingSurvey, exists := s.surveys[oldSurveyId]; exists {
		if existingSurvey.userId != userId {
			return false, errors.New("Diese Umfrage existiert bereits und wurde von einem anderen Benutzer erstellt!")
		}
		existingSurvey.Update(def, opt)
		return true, nil
	} else {
		return false, nil
	}
}

const IdLength = 30

func RandomString() string {
	from := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, IdLength)
	for i := range result {
		result[i] = from[rand.Intn(len(from))]
	}
	return string(result)
}

func (s *Surveys) getSurveyToVote(surveyId SurveyId) (*Survey, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	survey, exists := s.surveys[surveyId]
	return survey, exists
}

func (s *Surveys) getSurveyCheckUser(userId UserId, surveyId SurveyId) (*Survey, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	survey, exists := s.surveys[surveyId]
	if !exists {
		return nil, false
	}
	if survey.userId != userId {
		return nil, false
	}
	return survey, exists
}

func (s *Surveys) deleteSurvey(userId UserId, surveyId SurveyId) (*Survey, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	survey, exists := s.surveys[surveyId]
	if !exists {
		return nil, false
	}
	if survey.userId != userId {
		return nil, false
	}

	delete(s.surveys, surveyId)

	return survey, exists
}

func (s *Surveys) Clear(surveyId SurveyId, userId UserId) {
	survey, exists := s.deleteSurvey(userId, surveyId)
	if exists {
		survey.Lock()
		defer survey.Unlock()

		close(survey.changedNotify)

		log.Printf("deleted survey, %d surveys remaining\n", len(s.surveys))
	}
}

func (s *Surveys) GiveAwayQRCode(surveyId SurveyId, userId UserId) (string, error) {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return "", errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.userId != userId {
		return "", errors.New("Sie sind nicht der Ersteller dieser Umfrage!")
	}

	url := fmt.Sprintf("%s/?tuid=%s&tsid=%s", s.host, userId, surveyId)

	qrCode, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return "", fmt.Errorf("could not create qr code: %w", err)
	}

	return base64.StdEncoding.EncodeToString(qrCode), nil
}

func (s *Surveys) Uncover(userid UserId, surveyId SurveyId) error {
	survey, exists := s.getSurveyCheckUser(userid, surveyId)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.userId != userid {
		return errors.New("Sie sind nicht der Ersteller dieser Umfrage!")
	}

	votes := len(survey.votesCounted)
	if !s.debug && votes > 0 && votes <= 2 {
		return errors.New("Es sind noch nicht genug Stimmen abgegeben worden!")
	}

	survey.resultHidden = false
	survey.changed()
	return nil
}

func (s *Surveys) WaitForModification(userId UserId, surveyId SurveyId, clientVersion int) chan struct{} {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return nil
	}

	survey.Lock()
	defer survey.Unlock()

	if survey.version > clientVersion {
		// immediate notification if the version is higher than the client's version
		return closedChannel
	}

	return survey.changedNotify
}

func (s *Surveys) GetResult(userId UserId, surveyId SurveyId) Result {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return Result{Title: "Es gibt z.Z. keine Umfrage!", Version: -1}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Result()
}

func (s *Surveys) GetRunningSurvey(userId UserId, surveyId SurveyId) (SurveyQuestion, bool) {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return SurveyQuestion{}, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.question, true
}

func (s *Surveys) IsHiddenRunning(userId UserId, surveyId SurveyId) (bool, bool) {
	survey, exists := s.getSurveyCheckUser(userId, surveyId)
	if !exists {
		return false, false
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.resultHidden, true
}

func (s *Surveys) Vote(surveyId SurveyId, voterId UserId, option []int, number int) error {
	survey, exists := s.getSurveyToVote(surveyId)
	if !exists {
		return errors.New("Diese Umfrage existiert nicht!")
	}

	survey.Lock()
	defer survey.Unlock()

	if number != survey.number {
		return errors.New("Diese Umfrage war schon beendet!")
	}

	if !s.voteIfResultVisible {
		if !survey.resultHidden {
			return errors.New("Die Umfrageergebnisse sind bereits sichtbar!")
		}
	}

	if _, voted := survey.votesCounted[voterId]; voted {
		return errors.New("Sie haben bereits abgestimmt!")
	}

	survey.votesCounted[voterId] = struct{}{}

	for _, opt := range option {
		if opt < 0 || opt >= len(survey.options) {
			return errors.New("Ungültige Option!")
		}
		survey.options[opt].Votes++
	}

	survey.changed()

	return nil
}

func (s *Surveys) HasVoted(surveyId SurveyId, voterId UserId) bool {
	survey, exists := s.getSurveyToVote(surveyId)
	if !exists {
		return false
	}

	survey.Lock()
	defer survey.Unlock()

	_, voted := survey.votesCounted[voterId]
	return voted
}

func (s *Surveys) GetQuestion(surveyId SurveyId) Question {
	survey, exists := s.getSurveyToVote(surveyId)
	if !exists {
		return Question{Question: SurveyQuestion{Title: "Die Umfrage existiert nicht!"}}
	}

	survey.Lock()
	defer survey.Unlock()

	return survey.Question()
}

func (s *Surveys) startSurveyTimeoutCheck(timeOutInMin int) {
	surveyTimeout := time.Duration(timeOutInMin) * time.Minute
	go func() {
		log.Println("Starting survey cleanup routine, timeout", surveyTimeout)
		for {
			time.Sleep(surveyTimeout / 2)
			deleted, remaining := s.cleanup(surveyTimeout)
			if deleted > 0 {
				log.Printf("Deleted %d old surveys, %d surveys remaining\n", deleted, remaining)
			}
		}
	}()
}

func (s *Surveys) cleanup(surveyTimeout time.Duration) (int, int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	deleteCount := 0
	for id, survey := range s.surveys {
		if time.Since(survey.creationTime) > surveyTimeout {
			delete(s.surveys, id)
			deleteCount++
		}
	}

	return deleteCount, len(s.surveys)
}
