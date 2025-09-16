package logger

import (
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
)

type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Warn(msg string, args ...any)
}

var (
	infoLogger  *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger
)

func init() {
	id := uuid.New().String()
	prefixId := "[" + id + "] "
	infoLogger = log.New(os.Stdout, "[INFO] "+prefixId, log.Ldate|log.Ltime|log.Lshortfile)
	warnLogger = log.New(os.Stdout, "[WARN] "+prefixId, log.Ldate|log.Ltime|log.Lshortfile)
	errorLogger = log.New(os.Stdout, "[ERROR] "+prefixId, log.Ldate|log.Ltime|log.Lshortfile)

}
func Info(msg string, args ...any) {
	infoLogger.Output(2, fmt.Sprintf(msg, args...))
}

func Warn(msg string, args ...any) {
	warnLogger.Output(2, fmt.Sprintf(msg, args...))
}

func Error(msg string, args ...any) {
	errorLogger.Output(2, fmt.Sprintf(msg, args...))
}

func Fatal(args ...any) {
	errorLogger.Fatal(args) // logs + os.Exit(1)
}
