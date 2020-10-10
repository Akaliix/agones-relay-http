package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Octops/agones-event-broadcaster/pkg/events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RelayConfig struct {
	OnAddUrl       string
	OnUpdateUrl    string
	OnDeleteUrl    string
	WorkerReplicas int
}

type RelayRequest struct {
	Method    string
	Endpoints []string
	Payload   *Payload
}

type Payload struct {
	Body *events.Envelope
}

type RequestQueue struct {
	Name  string
	Queue chan *RelayRequest
}

type EventRelayRecord struct {
	Method       string
	URL          []string
	RequestQueue *RequestQueue
}

type EventRelayRegistry struct {
	Records map[string]*EventRelayRecord
}

type RelayHTTP struct {
	logger         *logrus.Entry
	wg             *sync.WaitGroup
	Client         Client
	Registry       *EventRelayRegistry
	Workers        []*Worker
	workerReplicas int
}

// TODO: Validate if URLs are valid http endpoints.
type Client func(req *http.Request) (*http.Response, error)

// TODO: Implement auth mechanism: BasicAuth
func NewRelayHTTP(logger *logrus.Entry, config RelayConfig, client Client) (*RelayHTTP, error) {
	applyConfigDefaults(&config)

	relay := &RelayHTTP{
		logger:         logger,
		wg:             &sync.WaitGroup{},
		Client:         client,
		Registry:       &EventRelayRegistry{},
		Workers:        make([]*Worker, config.WorkerReplicas*3), // 3 Events: OnAdd, OnUpdate, OnDelete
		workerReplicas: config.WorkerReplicas,
	}

	relay.Registry.Register(events.EventSourceOnAdd.String(), &EventRelayRecord{
		Method: http.MethodPost,
		URL:    strings.Split(config.OnAddUrl, ","),
		RequestQueue: &RequestQueue{
			Name:  "OnAdd",
			Queue: make(chan *RelayRequest, 1024),
		},
	})

	relay.Registry.Register(events.EventSourceOnUpdate.String(), &EventRelayRecord{
		Method: http.MethodPut,
		URL:    strings.Split(config.OnUpdateUrl, ","),
		RequestQueue: &RequestQueue{
			Name:  "OnUpdate",
			Queue: make(chan *RelayRequest, 1024),
		},
	})

	relay.Registry.Register(events.EventSourceOnDelete.String(), &EventRelayRecord{
		Method: http.MethodDelete,
		URL:    strings.Split(config.OnDeleteUrl, ","),
		RequestQueue: &RequestQueue{
			Name:  "OnDelete",
			Queue: make(chan *RelayRequest, 1024),
		},
	})

	return relay, nil
}

func (r *RelayHTTP) Start(ctx context.Context) error {
	r.InitWorkers(ctx, r.workerReplicas, r.Client)
	if err := r.StartWorkers(ctx); err != nil {
		r.logger.Fatal(errors.Wrap(err, "workers could not be started"))
	}

	<-ctx.Done()
	r.logger.Info("stopping Relay HTTP broker")
	r.wg.Wait()

	return nil
}

func (r *RelayHTTP) InitWorkers(ctx context.Context, replicas int, client Client) {
	count := 0
	for _, record := range r.Registry.Records {
		rr := record
		for i := 0; i < replicas; i++ {
			id := i + 1
			r.Workers[count] = NewWorker(rr.RequestQueue.Name+strconv.Itoa(id), rr.RequestQueue, client)
		}
		count++
	}
}

func (r *RelayHTTP) StartWorkers(ctx context.Context) error {
	for i := 0; i < len(r.Workers); i++ {
		r.wg.Add(1)
		i := i
		go func() {
			defer r.wg.Done()

			if err := r.Workers[i].Start(ctx); err != nil {
				r.logger.Fatal(errors.Wrap(err, "error starting worker"))
			}
		}()
	}

	return nil
}

// Called by the Broadcaster and builds the envelope that will be send as argument to the SendMessage function
func (r *RelayHTTP) BuildEnvelope(event events.Event) (*events.Envelope, error) {
	envelope := &events.Envelope{}

	envelope.AddHeader("event_source", event.EventSource().String())
	envelope.AddHeader("event_type", event.EventType().String())
	envelope.Message = event.(events.Message)

	return envelope, nil
}

// Called by the Broadcaster when a new event happens
func (r *RelayHTTP) SendMessage(envelope *events.Envelope) error {
	eventSource, err := getEventSourceHeader(envelope)
	if err != nil {
		return err
	}

	record, err := r.Registry.Get(eventSource)
	if err != nil {
		return errors.Wrap(err, "aborting sending message")
	}

	return r.EnqueueRequest(record.RequestQueue.Queue, createRequest(record, envelope))
}

func (r *RelayHTTP) EnqueueRequest(queue chan *RelayRequest, request *RelayRequest) error {
	select {
	case queue <- request:
	case <-time.After(5 * time.Second):
		return errors.New("request could not be enqueued due to timeout")
	}

	return nil
}

func (p *Payload) Read(b []byte) (n int, err error) {
	j, err := json.Marshal(p)
	if err != nil {
		return 0, errors.Wrap(io.ErrUnexpectedEOF, err.Error())
	}

	count := copy(b, j)
	return count, io.EOF
}

func (r *EventRelayRegistry) Register(eventSource string, record *EventRelayRecord) {
	if len(r.Records) == 0 {
		r.Records = map[string]*EventRelayRecord{}
	}

	r.Records[eventSource] = record
}

func (r *EventRelayRegistry) Get(eventSource string) (*EventRelayRecord, error) {
	if _, ok := r.Records[eventSource]; !ok {
		return nil, fmt.Errorf("event %q is not registry, sending aborted", eventSource)
	}

	return r.Records[eventSource], nil
}

func createRequest(record *EventRelayRecord, envelope *events.Envelope) *RelayRequest {
	request := &RelayRequest{
		Payload: &Payload{Body: envelope},
	}
	request.Method = record.Method
	request.Endpoints = record.URL

	return request
}

func applyConfigDefaults(config *RelayConfig) {
	if config.WorkerReplicas <= 0 {
		config.WorkerReplicas = 1
	}
}

func getEventSourceHeader(envelope *events.Envelope) (string, error) {
	if _, ok := envelope.Header.Headers["event_source"]; !ok {
		return "", errors.New("envelope header does not contain a valid event_source")
	}

	eventSource := envelope.Header.Headers["event_source"]
	return eventSource, nil
}
