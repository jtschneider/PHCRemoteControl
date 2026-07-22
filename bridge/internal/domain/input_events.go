package domain

type InputEvent int

const (
	EventPress       InputEvent = 2
	EventLongPress   InputEvent = 3
	EventRelease     InputEvent = 4
	EventDoublePress InputEvent = 5
)

func ShortPressEvents() []InputEvent {
	return []InputEvent{EventPress, EventRelease, EventDoublePress}
}

func LongPressEvents() []InputEvent {
	return []InputEvent{EventPress, EventLongPress}
}

func TipEvents() []InputEvent {
	return []InputEvent{EventPress, EventRelease}
}
