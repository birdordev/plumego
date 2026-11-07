package log

func (l *gLogger) Flush() {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, file := range l.logFiles {
		if file != nil {
			_ = file.Sync()
		}
	}
}

// SetRotationConfig sets the log rotation configuration
func (l *gLogger) SetRotationConfig(config rotationConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rotationConfig = config
}

// Close closes all log files and releases resources
func (l *gLogger) Close() {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	l.mu.Lock()
	defer l.mu.Unlock()

	for level, file := range l.logFiles {
		if file != nil {
			_ = file.Close()
			delete(l.logFiles, level)
			delete(l.currentSize, level)
		}
	}
	l.rebuildWriterCache()
}
