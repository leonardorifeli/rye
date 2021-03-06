package rye

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"context"
	"errors"
	"fmt"
	"github.com/InVisionApp/rye/fakes/statsdfakes"
	"github.com/onsi/gomega/types"
	"net/http"
	"net/http/httptest"
	"os"
	"time"
)

const (
	RYE_TEST_HANDLER_ENV_VAR = "RYE_TEST_HANDLER_PASS"
)

type statsInc struct {
	Name     string
	Time     int64
	StatRate float32
}

type statsTiming struct {
	Name     string
	Time     time.Duration
	StatRate float32
}

var _ = Describe("Rye", func() {

	var (
		request     *http.Request
		response    *httptest.ResponseRecorder
		mwHandler   *MWHandler
		fakeStatter *statsdfakes.FakeStatter
		inc         chan statsInc
		timing      chan statsTiming
	)

	const (
		STATRATE float32 = 1
	)

	BeforeEach(func() {
		fakeStatter = &statsdfakes.FakeStatter{}
		ryeConfig := Config{
			Statter:  fakeStatter,
			StatRate: STATRATE,
		}
		mwHandler = NewMWHandler(ryeConfig)

		response = httptest.NewRecorder()
		request = &http.Request{
			Header: make(map[string][]string, 0),
		}

		os.Unsetenv(RYE_TEST_HANDLER_ENV_VAR)

		inc = make(chan statsInc, 2)
		timing = make(chan statsTiming)

		fakeStatter.IncStub = func(name string, time int64, statrate float32) error {
			inc <- statsInc{name, time, statrate}
			return nil
		}

		fakeStatter.TimingDurationStub = func(name string, time time.Duration, statrate float32) error {
			timing <- statsTiming{name, time, statrate}
			return nil
		}

	})

	AfterEach(func() {
		os.Unsetenv(RYE_TEST_HANDLER_ENV_VAR)
	})

	Describe("NewMWHandler", func() {
		Context("when instantiating a mwhandler", func() {
			It("should have correct attributes", func() {
				ryeConfig := Config{
					Statter:  fakeStatter,
					StatRate: STATRATE,
				}
				handler := NewMWHandler(ryeConfig)
				Expect(handler).NotTo(BeNil())
				Expect(handler.Config.Statter).To(Equal(fakeStatter))
				Expect(handler.Config.StatRate).To(Equal(STATRATE))
			})

			It("should have attributes with default values when passed an empty config", func() {
				handler := NewMWHandler(Config{})
				Expect(handler).NotTo(BeNil())
				Expect(handler.Config.Statter).To(BeNil())
				Expect(handler.Config.StatRate).To(Equal(float32(0.0)))
			})
		})
	})

	Describe("Handle", func() {
		Context("when adding a valid handler", func() {
			It("should return valid HandlerFunc", func() {

				h := mwHandler.Handle([]Handler{successHandler})
				h.ServeHTTP(response, request)

				Expect(h).ToNot(BeNil())
				Expect(h).To(BeAssignableToTypeOf(func(http.ResponseWriter, *http.Request) {}))
				Expect(os.Getenv(RYE_TEST_HANDLER_ENV_VAR)).To(Equal("1"))

				Eventually(inc).Should(Receive(&statsInc{"handlers.successHandler.2xx", 1, float32(STATRATE)}))
				Eventually(timing).Should(Receive(HaveTiming("handlers.successHandler.runtime", float32(STATRATE))))
			})
		})

		Context("when a handler returns a response with StopExecution", func() {
			It("should not execute any further handlers", func() {
				request.Method = "OPTIONS"

				h := mwHandler.Handle([]Handler{stopExecutionHandler, successHandler})
				h.ServeHTTP(response, request)

				Expect(os.Getenv(RYE_TEST_HANDLER_ENV_VAR)).ToNot(Equal("1"))
			})
		})

		Context("when a handler returns a response with Context", func() {
			It("should add that new context to the next passed request", func() {
				h := mwHandler.Handle([]Handler{contextHandler, checkContextHandler})
				h.ServeHTTP(response, request)

				Expect(os.Getenv(RYE_TEST_HANDLER_ENV_VAR)).To(Equal("1"))
			})
		})

		Context("when a handler returns a response with neither error or StopExecution set", func() {
			It("should return a 500 + error message (and stop execution)", func() {
				h := mwHandler.Handle([]Handler{badResponseHandler, successHandler})
				h.ServeHTTP(response, request)

				Expect(response.Code).To(Equal(http.StatusInternalServerError))
				Expect(os.Getenv(RYE_TEST_HANDLER_ENV_VAR)).ToNot(Equal("1"))
			})
		})

		Context("when adding an erroneous handler", func() {
			It("should interrupt handler chain and set a response status code", func() {

				h := mwHandler.Handle([]Handler{failureHandler})
				h.ServeHTTP(response, request)

				Expect(h).ToNot(BeNil())
				Expect(h).To(BeAssignableToTypeOf(func(http.ResponseWriter, *http.Request) {}))
				Expect(response.Code).To(Equal(505))
				Eventually(inc).Should(Receive(&statsInc{"handlers.failureHandler.505", 1, float32(STATRATE)}))
				Eventually(inc).Should(Receive(&statsInc{"errors", 1, float32(STATRATE)}))
				Eventually(timing).Should(Receive(HaveTiming("handlers.failureHandler.runtime", float32(STATRATE))))
			})
		})

		Context("when the statter is not set", func() {
			It("should not call Inc or TimingDuration", func() {

				ryeConfig := Config{}
				handler := NewMWHandler(ryeConfig)

				h := handler.Handle([]Handler{successHandler})
				h.ServeHTTP(response, request)

				Expect(fakeStatter.IncCallCount()).To(Equal(0))
				Expect(fakeStatter.TimingDurationCallCount()).To(Equal(0))
			})
		})
	})

	Describe("getFuncName", func() {
		It("should return the name of the function as a string", func() {
			funcName := getFuncName(testFunc)
			Expect(funcName).To(Equal("testFunc"))
		})
	})

	Describe("Error()", func() {
		Context("when an error is set on Response struct", func() {
			It("should return a string if you call Error()", func() {
				resp := &Response{
					Err: errors.New("some error"),
				}

				Expect(resp.Error()).To(Equal("some error"))
			})
		})
	})
})

func contextHandler(rw http.ResponseWriter, r *http.Request) *Response {
	ctx := context.WithValue(r.Context(), "test-val", "exists")
	return &Response{Context: ctx}
}

func checkContextHandler(rw http.ResponseWriter, r *http.Request) *Response {
	testVal := r.Context().Value("test-val")
	if testVal == "exists" {
		os.Setenv(RYE_TEST_HANDLER_ENV_VAR, "1")
	}
	return nil
}

func successHandler(rw http.ResponseWriter, r *http.Request) *Response {
	os.Setenv(RYE_TEST_HANDLER_ENV_VAR, "1")
	return nil
}

func badResponseHandler(rw http.ResponseWriter, r *http.Request) *Response {
	return &Response{}
}

func failureHandler(rw http.ResponseWriter, r *http.Request) *Response {
	return &Response{
		StatusCode: 505,
		Err:        fmt.Errorf("Foo"),
	}
}

func stopExecutionHandler(rw http.ResponseWriter, r *http.Request) *Response {
	return &Response{
		StopExecution: true,
	}
}

func testFunc() {}

func HaveTiming(name string, statrate float32) types.GomegaMatcher {
	return WithTransform(
		func(p statsTiming) bool {
			return p.Name == name && p.StatRate == statrate
		}, BeTrue())
}
