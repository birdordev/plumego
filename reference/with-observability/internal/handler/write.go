package handler

import plumelog "github.com/birdordev/plumego/log"

// logWriteErr logs err at Warn level when it is non-nil.
// Pass the error returned by contract.WriteResponse or contract.WriteError.
func logWriteErr(logger plumelog.StructuredLogger, err error) {
	if err == nil || logger == nil {
		return
	}
	logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
}
