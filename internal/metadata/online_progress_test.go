package metadata_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func TestLookupProgressInResult(t *testing.T) {
	e, id := newLookupBook(t)
	res := e.lookup(id, online.Query{Title: "The Test Match", Author: "Ann Author"})
	if len(res.Steps) < 2 || res.Steps[0].Label != "Audible catalogue" || res.Steps[0].State != online.StepDone {
		t.Fatalf("steps in the result: %+v", res.Steps)
	}
	j := jobs.Job{State: jobs.StateRunning, Progress: []byte(`[{"provider":"audnexus","label":"Audible catalogue","state":"run"}]`)}
	if got := metadata.LookupSteps(j, metadata.LookupResult{}); len(got) != 1 || got[0].State != online.StepRunning {
		t.Errorf("running steps: %+v", got)
	}
	if got := metadata.LookupSteps(jobs.Job{State: jobs.StateRunning}, metadata.LookupResult{}); len(got) != 0 {
		t.Errorf("before the first step: %+v", got)
	}
}
