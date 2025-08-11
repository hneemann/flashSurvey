package survey

import (
	"sync"
	"testing"
)
import "github.com/stretchr/testify/assert"

// TestSync tests if the Surveys struct can handle concurrent voting without data
// races or inconsistencies.
// It creates 100 goroutines, each creating a survey and simulating 100 users
// voting on a survey.
// Run with `go test -race` to check for data races.
func TestSync(t *testing.T) {
	s := New("localhost", 30, false, false)
	wg := &sync.WaitGroup{}
	for range 100 {
		wg.Add(1)
		go voting(t, s, wg)
	}
	wg.Wait()
}

const voters = 100

var description = SurveyQuestion{
	Title:    "Test",
	Options:  []string{"Yes", "No"},
	Multiple: false,
}

func voting(t *testing.T, s *Surveys, mainWg *sync.WaitGroup) {
	userId := UserId(RandomString())
	sid, err := s.New(userId, "", description)
	assert.NoError(t, err)

	start := make(chan struct{})
	wg := sync.WaitGroup{}
	for range voters {
		wg.Add(1)
		go func() {
			voterId := UserId(RandomString())
			<-start
			err := s.Vote(sid, voterId, []int{1}, 1)
			assert.NoError(t, err)
			wg.Done()
		}()
	}

	close(start)
	wg.Wait()

	err = s.Uncover(userId, sid)
	assert.NoError(t, err)

	r := s.GetResult(userId, sid)
	assert.EqualValues(t, voters, r.Votes)
	assert.EqualValues(t, voters, r.Result[1].votes)

	s.Clear(sid, userId)
	mainWg.Done()
}
