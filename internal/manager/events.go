package manager

import (
	"reflect"
	"sync"
	"time"
)

type Event struct {
	ID      int64          `json:"id"`
	Type    EventType      `json:"type"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

const defaultEventBusCapacity = 1024

type eventBus struct {
	mu          sync.Mutex
	nextID      int64
	capacity    int
	ring        []Event
	closed      bool
	subscribers map[*eventSubscription]struct{}
}

type eventSubscription struct {
	C    chan Event
	once sync.Once
	bus  *eventBus
}

func newEventBus(capacity int) *eventBus {
	if capacity <= 0 {
		capacity = defaultEventBusCapacity
	}
	return &eventBus{
		capacity:    capacity,
		subscribers: map[*eventSubscription]struct{}{},
	}
}

func (b *eventBus) Publish(eventType EventType, payload map[string]any) Event {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Event{}
	}
	b.nextID++
	event := Event{
		ID:      b.nextID,
		Type:    eventType,
		At:      time.Now(),
		Payload: clonePayload(payload),
	}
	b.ring = append(b.ring, event)
	if len(b.ring) > b.capacity {
		b.ring = append([]Event(nil), b.ring[len(b.ring)-b.capacity:]...)
	}
	for sub := range b.subscribers {
		select {
		case sub.C <- cloneEvent(event):
		default:
		}
	}
	b.mu.Unlock()
	return event
}

func (b *eventBus) Replay(afterID int64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := []Event{}
	for _, event := range b.ring {
		if event.ID > afterID {
			out = append(out, cloneEvent(event))
		}
	}
	return out
}

func (b *eventBus) Subscribe(afterID int64) *eventSubscription {
	buffer := b.capacity
	if buffer < 64 {
		buffer = 64
	}

	sub := &eventSubscription{
		C:   make(chan Event, buffer),
		bus: b,
	}

	b.mu.Lock()
	if b.closed {
		sub.bus = nil
		close(sub.C)
		b.mu.Unlock()
		return sub
	}
	for _, event := range b.ring {
		if event.ID > afterID {
			sub.C <- cloneEvent(event)
		}
	}
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()

	return sub
}

func (s *eventSubscription) Close() {
	s.once.Do(func() {
		if s.bus == nil {
			return
		}
		s.bus.mu.Lock()
		delete(s.bus.subscribers, s)
		close(s.C)
		s.bus.mu.Unlock()
	})
}

func (b *eventBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*eventSubscription, 0, len(b.subscribers))
	for sub := range b.subscribers {
		subs = append(subs, sub)
		delete(b.subscribers, sub)
	}
	b.mu.Unlock()

	for _, sub := range subs {
		sub.Close()
	}
}

func cloneEvent(event Event) Event {
	event.Payload = clonePayload(event.Payload)
	return event
}

func clonePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(value)).Interface()
}

func cloneReflectValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneReflectValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(cloneReflectValue(value.Elem()))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneReflectValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.NumField(); i++ {
			field := out.Field(i)
			if field.CanSet() {
				field.Set(cloneReflectValue(value.Field(i)))
			}
		}
		return out
	default:
		return value
	}
}
