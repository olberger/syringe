package scheduler

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/nre-learning/syringe/api/exp/generated"
	config "github.com/nre-learning/syringe/config"

	// Fake clients
	kubernetesCrdFake "github.com/nre-learning/syringe/pkg/client/clientset/versioned/fake"
	kubernetesExtFake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	testclient "k8s.io/client-go/kubernetes/fake"
)

// Helper functions courtesy of the venerable Ben Johnson
// https://medium.com/@benbjohnson/structuring-tests-in-go-46ddee7a25c

// assert fails the test if the condition is false.
func assert(tb testing.TB, condition bool, msg string, v ...interface{}) {
	if !condition {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: "+msg+"\033[39m\n\n", append([]interface{}{filepath.Base(file), line}, v...)...)
		tb.FailNow()
	}
}

// ok fails the test if an err is not nil.
func ok(tb testing.TB, err error) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected error: %s\033[39m\n\n", filepath.Base(file), line, err.Error())
		tb.FailNow()
	}
}

// equals fails the test if exp is not equal to act.
func equals(tb testing.TB, exp, act interface{}) {
	if !reflect.DeepEqual(exp, act) {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d:\n\n\texp: %#v\n\n\tgot: %#v\033[39m\n\n", filepath.Base(file), line, exp, act)
		tb.FailNow()
	}
}

type fakeHealthChecker struct{}

func (lhc *fakeHealthChecker) sshTest(host string, port int) bool { return true }
func (lhc *fakeHealthChecker) tcpTest(host string, port int) bool { return true }

func createFakeScheduler() *LessonScheduler {
	os.Setenv("SYRINGE_CURRICULUM", "foo")
	os.Setenv("SYRINGE_DOMAIN", "bar")
	syringeConfig, err := config.LoadConfigVars()
	if err != nil {
		// t.Fatal(err)
		panic(err)
	}

	var lessons = map[int32]*pb.Lesson{
		1: &pb.Lesson{
			LessonId: 1,
			Stages: []*pb.LessonStage{
				{
					Id:          0,
					Description: "",
				},
				{
					Id:          1,
					Description: "foobar",
				},
			},
			LessonName: "Test Lesson",
			Endpoints: []*pb.Endpoint{
				{
					Name:  "vqfx1",
					Image: "antidotelabs/vqfx",
					Presentations: []*pb.Presentation{
						{Name: "cli", Type: "ssh", Port: 22},
					},
				},
				{
					Name:  "vqfx2",
					Image: "antidotelabs/vqfx",
					Presentations: []*pb.Presentation{
						{Name: "cli", Type: "ssh", Port: 22},
					},
				},
				{
					Name:  "vqfx3",
					Image: "antidotelabs/vqfx",
					Presentations: []*pb.Presentation{
						{Name: "cli", Type: "ssh", Port: 22},
					},
				},
				{
					Name:  "linux1",
					Image: "antidotelabs/utility",
					Presentations: []*pb.Presentation{
						{Name: "cli", Type: "ssh", Port: 22},
					},
				},
				{
					Name:  "web1",
					Image: "antidotelabs/webserver",
					Presentations: []*pb.Presentation{
						{Name: "webui", Type: "http", Port: 80},
					},
				},
			},
			Connections: []*pb.Connection{
				{
					A: "vqfx1",
					B: "vqfx2",
				},
				{
					A: "vqfx2",
					B: "vqfx3",
				},
				{
					A: "vqfx3",
					B: "vqfx1",
				},
			},
			Category: "fundamentals",
			Tier:     "prod",
		},
	}

	curriculum := &pb.Curriculum{
		Lessons: lessons,
	}

	nsName := "1-foobar-ns"
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			// Namespace: nsName,
		},
	}

	// Start lesson scheduler
	lessonScheduler := LessonScheduler{
		// KubeConfig:    kubeConfig,
		Requests:      make(chan *LessonScheduleRequest),
		Results:       make(chan *LessonScheduleResult),
		Curriculum:    curriculum,
		SyringeConfig: syringeConfig,
		GcWhiteList:   make(map[string]*pb.Session),
		GcWhiteListMu: &sync.Mutex{},
		KubeLabs:      make(map[string]*KubeLab),
		KubeLabsMu:    &sync.Mutex{},

		DisableGC: true,

		Client:    testclient.NewSimpleClientset(namespace),
		ClientExt: kubernetesExtFake.NewSimpleClientset(),
		ClientCrd: kubernetesCrdFake.NewSimpleClientset(),

		HealthChecker: &fakeHealthChecker{},
	}

	return &lessonScheduler
}

func TestSchedulerSetup(t *testing.T) {

	lessonScheduler := createFakeScheduler()

	// Start scheduler
	go func() {
		err := lessonScheduler.Start()
		if err != nil {
			t.Fatalf("Problem starting lesson scheduler: %s", err)
		}
	}()

	go func() {
		for {
			result := <-lessonScheduler.Results
			// log.Info(result)

			if !result.Success && result.Operation == OperationType_CREATE {
				t.Fatal("Received error from scheduler")
			}
		}
	}()

	anHourAgo := time.Now().Add(time.Duration(-1) * time.Hour)

	numberKubeLabs := 5
	for i := 1; i <= numberKubeLabs; i++ {
		uuid, _ := newUUID()
		req := &LessonScheduleRequest{
			Lesson:    lessonScheduler.Curriculum.Lessons[1],
			Operation: OperationType_CREATE,
			Stage:     1,
			Uuid:      uuid,
			Created:   anHourAgo,
		}
		lessonScheduler.Requests <- req
	}

	time.Sleep(time.Second * 10)

	for k := range lessonScheduler.KubeLabs {
		kl := lessonScheduler.KubeLabs[k]

		assert(t, (len(kl.Pods) == 5),
			fmt.Sprintf("Pods count mismatch: %d", len(kl.Pods)))
		assert(t, (len(kl.Services) == 5),
			fmt.Sprintf("Services count mismatch: %d", len(kl.Services)))
		assert(t, (len(kl.Ingresses) == 1),
			fmt.Sprintf("Ingress count mismatch: %d", len(kl.Ingresses)))
		netps, _ := lessonScheduler.Client.NetworkingV1().NetworkPolicies(fmt.Sprintf("%s-ns", kl.CreateRequest.Uuid)).List(metav1.ListOptions{})
		assert(t, (len(netps.Items) == 1),
			fmt.Sprintf("Must have a single networkpolicy - %s", fmt.Sprintf("%s-ns", kl.CreateRequest.Uuid)))
	}

	if len(lessonScheduler.KubeLabs) != numberKubeLabs {
		t.Fatalf("Not the expected number of kubelabs (expected %d, got %d)", numberKubeLabs, len(lessonScheduler.KubeLabs))
	}
	// TODO(mierdin): Need to create a fake health check tester

	cleaned, err := lessonScheduler.PurgeOldLessons()
	ok(t, err)

	// time.Sleep(time.Second * 5)

	assert(t, (len(cleaned) == numberKubeLabs),
		fmt.Sprintf("got %d cleaned lessons, expected %d", len(cleaned), numberKubeLabs))
	// assert(t, (cleaned[0] == "100-foobar-ns"), "")

}

// newUUID generates a random UUID according to RFC 4122
func newUUID() (string, error) {
	uuid := make([]byte, 16)
	n, err := io.ReadFull(rand.Reader, uuid)
	if n != len(uuid) || err != nil {
		return "", err
	}
	// variant bits; see section 4.1.1
	uuid[8] = uuid[8]&^0xc0 | 0x80
	// version 4 (pseudo-random); see section 4.1.3
	uuid[6] = uuid[6]&^0xf0 | 0x40
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]), nil
}
