package log4g

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TimeFormat = "2006-01-02 15:04:05.000"

	accessFilename = "access.log"
	errorFilename  = "error.log"
	slowFilename   = "slow.log"
	statFilename   = "stat.log"

	consoleMode = "console"
	varMode     = "var"

	defaultHostName = "log4g"

	infoPrefix          = "[INFO] "
	errorPrefix         = "[ERROR] "
	slowPrefix          = "[SLOW]"
	backupFileDelimiter = "-"
	callerInnerDepth    = 5
	flags               = 0x0
)

var (
	ErrLogPathNotSet      = errors.New("log path must be set")
	ErrLogNotInitialized  = errors.New("log not initialized")
	ErrLogNameSpaceNotSet = errors.New("log service name must be set")

	writeConsole bool
	infoLog      io.WriteCloser
	errorLog     io.WriteCloser
	slowLog      io.WriteCloser
	statLog      io.WriteCloser
	stackLog     *LessLogger

	once        sync.Once
	initialized uint32
	options     logOptions
)

type (
	logOptions struct {
		gzipEnabled           bool
		logStackCoolDownMills int
		keepDays              int
	}

	LogOption func(options *logOptions)

	Logger interface {
		Error(...interface{})
		ErrorFormat(string, ...interface{})
		Info(...interface{})
		InfoFormat(string, ...interface{})
	}
)

func Init(c Config) {
	if err := SetUp(c); err != nil {
		log.Fatal(err)
	}
}

func SetUp(c Config) error {
	switch c.LogMode {
	case consoleMode:
		setupWithConsole()
		return nil
	case varMode:
		return setupWithVolume(c)
	default:
		return setupWithFiles(c)
	}
}

func AddTime(msg string) string {
	now := []byte(time.Now().Format(TimeFormat))
	msgBytes := []byte(msg)
	buf := make([]byte, len(now)+1+len(msgBytes))
	n := copy(buf, now)
	buf[n] = ' '
	copy(buf[n+1:], msgBytes)

	return string(buf)
}

func AddTimeAndCaller(msg string, callDepth int) string {
	var buf strings.Builder

	buf.WriteString(time.Now().Format(TimeFormat))
	buf.WriteByte(' ')

	caller := getCaller(callDepth)
	if len(caller) > 0 {
		buf.WriteString(caller)
		buf.WriteByte(' ')
	}

	buf.WriteString(msg)

	return buf.String()
}

func Close() error {
	if writeConsole {
		return nil
	}

	if atomic.LoadUint32(&initialized) == 0 {
		return ErrLogNotInitialized
	}

	atomic.StoreUint32(&initialized, 0)

	if infoLog != nil {
		if err := infoLog.Close(); err != nil {
			return err
		}
	}

	if errorLog != nil {
		if err := errorLog.Close(); err != nil {
			return err
		}
	}

	if slowLog != nil {
		if err := slowLog.Close(); err != nil {
			return err
		}
	}

	if statLog != nil {
		if err := statLog.Close(); err != nil {
			return err
		}
	}

	return nil
}

func Error(v ...interface{}) {
	ErrorCaller(1, v...)
}

func ErrorFormat(format string, v ...interface{}) {
	ErrorCallerFormat(1, format, v...)
}

func ErrorCaller(callDepth int, v ...interface{}) {
	errorSync(fmt.Sprintln(v...), callDepth+callerInnerDepth)
}

func ErrorCallerFormat(callDepth int, format string, v ...interface{}) {
	errorSync(fmt.Sprintf(fmt.Sprintf("%s\n", format), v...), callDepth+callerInnerDepth)
}

func Info(v ...interface{}) {
	infoSync(fmt.Sprintln(v...))
}

func InfoFormat(format string, v ...interface{}) {
	infoSync(fmt.Sprintf(fmt.Sprintf("%s\n", format), v...))
}

func Server(v ...interface{}) {
	stackSync(fmt.Sprint(v...))
}

func ServerFormat(format string, v ...interface{}) {
	stackSync(fmt.Sprintf(format, v...))
}

func Slow(v ...interface{}) {
	slowSync(fmt.Sprintln(v...))
}

func SlowFormat(format string, v ...interface{}) {
	slowSync(fmt.Sprintf(fmt.Sprintf("%s\n", format), v...))
}

func Stat(v ...interface{}) {
	statSync(fmt.Sprintln(v...))
}

func StatFormat(format string, v ...interface{}) {
	statSync(fmt.Sprintf(fmt.Sprintf("%s\n", format), v...))
}

func WithCoolDownMillis(millis int) LogOption {
	return func(opts *logOptions) {
		opts.logStackCoolDownMills = millis
	}
}

func WithKeepDays(days int) LogOption {
	return func(opts *logOptions) {
		opts.keepDays = days
	}
}

func WithGzip() LogOption {
	return func(opts *logOptions) {
		opts.gzipEnabled = true
	}
}

func createOutput(path string) (io.WriteCloser, error) {
	if len(path) == 0 {
		return nil, ErrLogPathNotSet
	}
	return NewLogger(path, DefaultBackupRule(path, backupFileDelimiter, options.keepDays,
		options.gzipEnabled), options.gzipEnabled)
}

func errorSync(msg string, callDepth int) {
	if atomic.LoadUint32(&initialized) == 0 {
		outputError(nil, msg, callDepth)
	} else {
		outputError(errorLog, msg, callDepth)
	}
}

func getCaller(callDepth int) string {
	var buf strings.Builder
	_, file, line, ok := runtime.Caller(callDepth)
	if ok {
		short := file
		for i := len(file) - 1; i > 0; i-- {
			if file[i] == '/' {
				short = file[i+1:]
				break
			}
		}
		buf.WriteString(short)
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(line))
	}

	return buf.String()
}

func handleOptions(opts []LogOption) {
	for _, opt := range opts {
		opt(&options)
	}
}

func infoSync(msg string) {
	if atomic.LoadUint32(&initialized) == 0 {
		output(nil, msg)
	} else {
		output(infoLog, msg)
	}
}

func output(writer io.Writer, msg string) {
	buf := AddTime(msg)
	if writer != nil {
		if _, err := writer.Write([]byte(buf)); err != nil {
			log.Println(err)
		}
	} else {
		log.Print(buf)
	}
}

func outputError(writer io.Writer, msg string, callDepth int) {
	content := AddTimeAndCaller(msg, callDepth)
	if writer != nil {
		if _, err := writer.Write([]byte(content)); err != nil {
			log.Println(err)
		}
	} else {
		log.Print(content)
	}
}

func setupWithConsole() {
	writeConsole = true
	once.Do(func() {
		infoLog = newLogWriter(log.New(os.Stdout, infoPrefix, flags))
		errorLog = newLogWriter(log.New(os.Stderr, errorPrefix, flags))
		slowLog = newLogWriter(log.New(os.Stderr, slowPrefix, flags))
		statLog = infoLog
		atomic.StoreUint32(&initialized, 1)
	})
}

func setupWithFiles(c Config) error {
	var opts []LogOption
	var err error

	if len(c.Path) == 0 {
		return ErrLogPathNotSet
	}

	opts = append(opts, WithCoolDownMillis(c.StackCoolDownMillis))
	if c.Compress {
		opts = append(opts, WithGzip())
	}
	if c.KeepDays > 0 {
		opts = append(opts, WithKeepDays(c.KeepDays))
	}

	accessFile := path.Join(c.Path, accessFilename)
	errorFile := path.Join(c.Path, errorFilename)
	slowFile := path.Join(c.Path, slowFilename)
	statFile := path.Join(c.Path, statFilename)

	once.Do(func() {
		handleOptions(opts)

		if infoLog, err = createOutput(accessFile); err != nil {
			return
		}

		if errorLog, err = createOutput(errorFile); err != nil {
			return
		}

		if slowLog, err = createOutput(slowFile); err != nil {
			return
		}

		if statLog, err = createOutput(statFile); err != nil {
			return
		}

		stackLog = NewLessLogger(options.logStackCoolDownMills)
		atomic.StoreUint32(&initialized, 1)
	})

	return err
}

func setupWithVolume(c Config) error {
	if len(c.NameSpace) == 0 {
		return ErrLogNameSpaceNotSet
	}

	hostname := getHostname()
	c.Path = path.Join(c.Path, c.NameSpace, hostname)

	return setupWithFiles(c)
}

func slowSync(msg string) {
	if atomic.LoadUint32(&initialized) == 0 {
		output(nil, msg)
	} else {
		output(slowLog, msg)
	}
}

func stackSync(msg string) {
	if atomic.LoadUint32(&initialized) == 0 {
		output(nil, fmt.Sprintf("%s\n%s", msg, string(debug.Stack())))
	} else {
		stackLog.Errorf("%s\n%s", msg, string(debug.Stack()))
	}
}

func statSync(msg string) {
	if atomic.LoadUint32(&initialized) == 0 {
		output(nil, msg)
	} else {
		output(statLog, msg)
	}
}

type logWriter struct {
	logger *log.Logger
}

func newLogWriter(logger *log.Logger) logWriter {
	return logWriter{
		logger: logger,
	}
}

func (lw logWriter) Close() error {
	return nil
}

func (lw logWriter) Write(data []byte) (int, error) {
	lw.logger.Print(string(data))
	return len(data), nil
}

// find host name
// will use default host name if not found
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil || len(hostname) == 0 {
		return defaultHostName
	}

	return hostname
}
