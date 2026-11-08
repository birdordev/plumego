package handler

import plumelog "github.com/birdordev/plumego/log"

// logWriteErr logs err at Warn level when it is non-nil.
// Pass the error returned by contract.WriteResponse or contract.WriteError.
// Response write errors (e.g., client disconnects after headers are sent)
// are not recoverable; logging them surfaces unexpected disconnects for operators.
func logWriteErr(logger plumelog.StructuredLogger, err error) {
	if err == nil {
		return
	}
	logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
}
