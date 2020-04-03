package service

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"reflect"
	"sync"
)

var errNoConfig = fmt.Errorf("no config has been provided")
var errTempFix223 = fmt.Errorf("temporary error for fix #223")

const InitMethod = "Init"

//Service can serve. Services can provide Init method which must return (bool, error) signature and might accept
type Service interface {
	//Serve serves.
	Serve() error

	//Detach stops the service.
	Stop()
}

//container controls all internal RR services and provides plugin based system.
type Container interface {
	//Register add new service to the container under given name.
	Register(name string, service interface{})

	//Reconfigure configures all underlying services with given configuration.
	Init(cfg Config) error

	//Check if svc has been registered.
	Has(service string) bool

	//get returns svc instance by it's name or nil if svc not found. Method returns current service status
	//as second value.
	Get(service string) (svc interface{}, status int)

	//Serve all configured services. Non blocking.
	Serve() error

	//Close all active services.
	Stop()

	//List service names.
	List() []string
}

//Config provides ability to slice configuration sections and unmarshal configuration data into
type Config interface {
	// get nested config section (sub-map), returns nil if section not found.
	Get(service string) Config

	//Unmarshal unmarshal config data into given struct.
	Unmarshal(out interface{}) error
}

type container struct {
	mu       sync.Mutex
	log      logrus.FieldLogger
	services []*service

	errors	chan struct {
		name string
		err  error
	}
}

func NewContainer(log logrus.FieldLogger) Container {
	return &container{
		log:      log,
		services: make([]*service, 0),
		errors: make(chan struct {
			name string
			err  error
		}, 1),
	}
}

func (c *container) Register(name string, serviceItem interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.services = append(c.services, &service{
		name:   name,
		svc:    serviceItem,
		status: StatusInactive,
	})
}

func (c *container) Has(target string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.services {
		if e.name == target {
			return true
		}
	}

	return false
}

func (c *container) Get(target string) (svc interface{}, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.services {
		if e.name == target {
			return e.svc, e.getStatus()
		}
	}

	return nil, StatusUndefined
}

func (c *container) Init(cfg Config) error {
	for _, e := range c.services {
		if e.getStatus() >= StatusOK {
			return fmt.Errorf("service [%s] has already been configured", e.name)
		}

		//inject service dependencies
		if ok, err := c.initService(e.svc, cfg.Get(e.name)); err != nil {
			//soft error (skipping)
			if err == errNoConfig {
				c.log.Debugf("[%s]: disabled", e.name)
				continue
			}

			return errors.Wrap(err, fmt.Sprintf("[%s]", e.name))
		} else if ok {
			e.setStatus(StatusOK)
		} else {
			c.log.Debugf("[%s]: disabled", e.name)
		}
	}

	return nil
}

func (c *container) Serve() error {
	var running = 0

	for _, e := range c.services {
		if e.hasStatus(StatusOK) && e.canServe() {
			running++
			c.log.Debugf("[%s]: started", e.name)
			go func(e *service) {
				e.setStatus(StatusServing)
				defer e.setStatus(StatusStopped)

				if err := e.svc.(Service).Serve(); err != nil {
					c.errors <- struct {
						name string
						err  error
					}{name: e.name, err: errors.Wrap(err, fmt.Sprintf("[%s]", e.name))}
				} else {
					c.errors <- struct {
						name string
						err  error
					}{name: e.name, err: errTempFix223}
				}
			}(e)
		}
	}

	if running == 0 {
		return nil
	}

	for fail := range c.errors {
		if fail.err == errTempFix223 {
			// if we call stop, then stop all plugins
			break
		} else {
			c.log.Errorf("[%s]: %s", fail.name, fail.err)
			c.Stop()

			return fail.err
		}
	}

	return nil
}

func (c *container) Stop() {
	for _, e := range c.services {
		if e.hasStatus(StatusServing) {
			e.setStatus(StatusStopping)
			e.svc.(Service).Stop()
			e.setStatus(StatusStopped)

			c.log.Debugf("[%s]: stopped", e.name)
		}
	}
}

func (c *container) List() []string {
	names := make([]string, 0, len(c.services))
	for _, e := range c.services {
		names = append(names, e.name)
	}

	return names
}

func (c *container) initService(s interface{}, segment Config) (bool, error) {
	r := reflect.TypeOf(s)

	m, ok := r.MethodByName(InitMethod)
	if !ok {
		return true, nil
	}

	if err := c.verifySignature(m); err != nil {
		return false, err
	}

	values, err := c.resolveValues(s, m, segment)
	if err != nil {
		return false, err
	}

	out := m.Func.Call(values)

	if out[1].IsNil() {
		return out[0].Bool(), nil
	}

	return out[0].Bool(), out[1].Interface().(error)
}

func (c *container) resolveValues(s interface{}, m reflect.Method, cfg Config) (values []reflect.Value, err error) {
	for i := 0; i < m.Type.NumIn(); i++ {
		v := m.Type.In(i)

		switch {
			case v.ConvertibleTo(reflect.ValueOf(s).Type()): //service itself
				values = append(values, reflect.ValueOf(s))

			case v.Implements(reflect.TypeOf((*Container)(nil)).Elem()): //container
				values = append(values, reflect.ValueOf(c))

			case v.Implements(reflect.TypeOf((*logrus.StdLogger)(nil)).Elem()),
				v.Implements(reflect.TypeOf((*logrus.FieldLogger)(nil)).Elem()),
				v.ConvertibleTo(reflect.ValueOf(c.log).Type()): //logger
				values = append(values, reflect.ValueOf(c.log))

			default: //dependency on other service (resolution to nil if service can't be found)
				value, err := c.resolveValue(v)
				if err != nil {
					return nil, err
				}

				values = append(values, value)
		}
	}

	return
}

func (c *container) verifySignature(m reflect.Method) error {
	if m.Type.NumOut() != 2 {
		return fmt.Errorf("method Init must have exact 2 return values")
	}

	if m.Type.Out(0).Kind() != reflect.Bool {
		return fmt.Errorf("first return value of Init method must be bool type")
	}

	if !m.Type.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return fmt.Errorf("second return value of Init method value must be error type")
	}

	return nil
}

func (c *container) resolveValue(v reflect.Type) (reflect.Value, error) {
	value := reflect.Value{}
	for _, e := range c.services {
		if !e.hasStatus(StatusOK) {
			continue
		}

		if v.Kind() == reflect.Interface && reflect.TypeOf(e.svc).Implements(v) {
			if value.IsValid() {
				return value, fmt.Errorf("disambiguous dependency `%s`", v)
			}

			value = reflect.ValueOf(e.svc)
		}

		if v.ConvertibleTo(reflect.ValueOf(e.svc).Type()) {
			if value.IsValid() {
				return value, fmt.Errorf("disambiguous dependency `%s`", v)
			}

			value = reflect.ValueOf(e.svc)
		}
	}

	if !value.IsValid() {
		//placeholder (make sure to check inside the method)
		value = reflect.New(v).Elem()
	}

	return value, nil
}
