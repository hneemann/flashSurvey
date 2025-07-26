package main

import (
	"context"
	"flag"
	"flashSurvey/handler"
	"flashSurvey/survey"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func main() {
	host := flag.String("host", "", "The host which is seen externally.")
	cert := flag.String("cert", "", "certificate pem")
	key := flag.String("key", "", "certificate key")
	debug := flag.Bool("debug", false, "debug mode")
	port := flag.Int("port", 8080, "port")
	flag.Parse()

	http.HandleFunc("/", handler.EnsureId(handler.Create(*host, *debug)))
	http.HandleFunc("/result/", handler.EnsureId(handler.Result))
	http.HandleFunc("/resultRest/", handler.EnsureId(handler.ResultRest))
	http.HandleFunc("/vote/", handler.EnsureId(handler.Vote))
	http.HandleFunc("/voteRest/", handler.EnsureId(handler.VoteRest))

	serv := &http.Server{Addr: ":" + strconv.Itoa(*port)}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-c
		log.Print("terminated by signal ", sig.String())

		err := serv.Shutdown(context.Background())
		if err != nil {
			log.Println(err)
		}
		for {
			<-c
		}
	}()

	survey.StartSurveyCheck()

	var err error
	if *cert != "" && *key != "" {
		log.Println("Starting server with TLS")
		err = serv.ListenAndServeTLS(*cert, *key)
	} else {
		log.Println("Starting server without TLS")
		err = serv.ListenAndServe()
	}
	if err != nil {
		log.Println(err)
	}

}
