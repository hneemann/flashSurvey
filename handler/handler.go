package handler

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flashSurvey/survey"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*
var templateFS embed.FS
var Templates = template.Must(template.New("").ParseFS(templateFS, "templates/*.html"))
var createTemp = Templates.Lookup("create.html")
var resultTemp = Templates.Lookup("result.html")
var voteTemp = Templates.Lookup("vote.html")
var resultTableTemp = Templates.Lookup("resultTable.html")
var voteNotifyTemp = Templates.Lookup("voteNotify.html")
var voteQuestionTemp = Templates.Lookup("voteQuestion.html")

func EnsureId(handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var id string
		c, err := request.Cookie("surveyId")
		if err == nil {
			id = c.Value
		} else {
			id = randomString()
			c = &http.Cookie{
				Name:  "surveyId",
				Value: id,
			}
			http.SetCookie(writer, c)
		}
		request = request.WithContext(context.WithValue(request.Context(), "id", id))
		handler(writer, request)
	}
}

func randomString() string {
	from := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length := 30
	result := make([]byte, length)
	for i := range result {
		result[i] = from[rand.Intn(len(from))]
	}
	return string(result)
}

type CreateData struct {
	SurveyID survey.SurveyID
	Title    string
	Multiple bool
	Options  []string
}

func (d CreateData) URL() string {
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
	return "?q=" + template.URLQueryEscaper(str)
}

func (d CreateData) clean(o string) string {
	o = strings.TrimSpace(o)
	o = strings.ReplaceAll(o, ";", "")
	return o
}

func Create(host string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		surveyId := survey.SurveyID(request.Context().Value("id").(string))

		var d CreateData

		q := request.URL.Query().Get("q")
		str := strings.Split(q, ";")
		if len(str) > 3 {
			o := make([]string, 6)
			for i, s := range str[2:] {
				if i < len(o) {
					o[i] = s
				} else {
					break
				}
			}
			d = CreateData{
				SurveyID: surveyId,
				Title:    str[0],
				Multiple: str[1] == "m",
				Options:  o,
			}
		} else {
			d = CreateData{
				SurveyID: surveyId,
				Title:    "Die Erklärung habe ich verstanden!",
				Options:  []string{"Ja", "Teilweise", "Nein", "", "", ""},
			}
		}

		if request.Method == http.MethodPost {
			err := request.ParseForm()
			if err != nil {
				http.Error(writer, "could not parse form: "+err.Error(), http.StatusBadRequest)
				return
			}
			title := request.FormValue("title")
			d.Title = title
			var options []string
			for i := range 6 {
				o := request.FormValue("option" + strconv.Itoa(i+1))
				if o != "" {
					options = append(options, o)
				}
				d.Options[i] = o
			}
			multiple := request.FormValue("multiple") == "true"
			d.Multiple = multiple

			err = survey.New(host, surveyId, title, multiple, options...)
			if err != nil {
				http.Error(writer, "could not create survey: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		err := createTemp.Execute(writer, d)
		if err != nil {
			http.Error(writer, "could not execute template: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

type ResultData struct {
	SurveyID survey.SurveyID `json:"-"`
	QRCode   string          `json:"-"`
	Title    string          `json:"Title"`
	Result   template.HTML   `json:"Result"`
}

func dataFromResult(surveyId survey.SurveyID, result survey.Result) ResultData {
	var b bytes.Buffer
	err := resultTableTemp.Execute(&b, result.Result)
	if err != nil {
		log.Println("could not execute result table template:", err)
	}
	return ResultData{
		SurveyID: surveyId,
		QRCode:   result.QRCode,
		Title:    result.Title,
		Result:   template.HTML(b.String()),
	}
}

func Result(writer http.ResponseWriter, request *http.Request) {
	surveyId := survey.SurveyID(request.Context().Value("id").(string))
	result := survey.GetResult(surveyId)

	data := dataFromResult(surveyId, result)

	err := resultTemp.Execute(writer, data)
	if err != nil {
		log.Println(err)
	}
}

func ResultRest(writer http.ResponseWriter, request *http.Request) {
	surveyId := survey.SurveyID(request.Context().Value("id").(string))
	result := survey.GetResult(surveyId)

	jsonData, err := json.Marshal(dataFromResult(surveyId, result))
	if err != nil {
		http.Error(writer, "could not marshal result: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	_, err = writer.Write(jsonData)
	if err != nil {
		log.Println(err)
	}
}

func Vote(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	surveyId := survey.SurveyID(query.Get("id"))

	question := survey.GetQuestion(surveyId)
	err := voteTemp.Execute(writer, question)
	if err != nil {
		log.Println(err)
	}
}

func VoteRest(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	surveyId := survey.SurveyID(query.Get("id"))
	option := query.Get("o")
	var o []int
	for _, s := range strings.Split(option, ",") {
		oi, err := strconv.Atoi(s)
		if err == nil {
			o = append(o, oi)
		}
	}
	if len(o) > 0 {
		number := query.Get("n")
		n, err := strconv.Atoi(number)
		if err == nil && n >= 0 {
			voterId := survey.VoterID(request.Context().Value("id").(string))
			err = survey.Vote(surveyId, voterId, o, n)
			type voted struct {
				SurveyID survey.SurveyID
				Error    error
			}

			err = voteNotifyTemp.Execute(writer, voted{
				Error: err,
			})
		}
	} else {

		question := survey.GetQuestion(surveyId)
		err := voteQuestionTemp.Execute(writer, question)
		if err != nil {
			log.Println(err)
		}
	}

}
