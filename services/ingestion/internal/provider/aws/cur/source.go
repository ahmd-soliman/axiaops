package cur

import (
	"errors"
	"time"
)

var (
	ErrQueryFailed   = errors.New("athena query failed")
	ErrQueryCanceled = errors.New("athena query canceled")
)

type AthenaCURSource struct {
	client       AthenaAPI
	database     string
	table        string
	workgroup    string
	resultsS3    string
	pollInterval time.Duration
}

func NewAthenaCURSource(client AthenaAPI, database, table, workgroup, resultsS3 string) *AthenaCURSource {
	return &AthenaCURSource{
		client:       client,
		database:     database,
		table:        table,
		workgroup:    workgroup,
		resultsS3:    resultsS3,
		pollInterval: 2 * time.Second, // adjustable for tests
	}
}

func (s *AthenaCURSource) Name() string {
	return "aws-cur-athena"
}

