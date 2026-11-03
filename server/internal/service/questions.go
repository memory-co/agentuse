package service

import "errors"

var ErrQuestionClosed = errors.New("request is no longer awaiting a reply")
