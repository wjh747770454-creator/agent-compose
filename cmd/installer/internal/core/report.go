package core

type EventKind string

const (
	EventInfo    EventKind = "info"
	EventWarning EventKind = "warning"
	EventStep    EventKind = "step"
)

type Event struct {
	Kind    EventKind
	Message string
}

type Reporter interface {
	Report(Event)
}

type ReporterFunc func(Event)

func (f ReporterFunc) Report(event Event) {
	if f != nil {
		f(event)
	}
}

type discardReporter struct{}

func (discardReporter) Report(Event) {}
