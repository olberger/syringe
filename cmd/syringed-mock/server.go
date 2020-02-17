package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/golang/glog"
	swag "github.com/nre-learning/syringe/api/exp/swagger"
	config "github.com/nre-learning/syringe/config"

	"github.com/nre-learning/syringe/pkg/ui/data/swagger"

	ghandlers "github.com/gorilla/handlers"
	runtime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	pb "github.com/nre-learning/syringe/api/exp/generated"
	assetfs "github.com/philips/go-bindata-assetfs"
	log "github.com/sirupsen/logrus"
	grpc "google.golang.org/grpc"

	gw "github.com/nre-learning/syringe/api/exp/generated"
    
    lti "github.com/jordic/lti"
    "flag"
)

var (
	secret      = flag.String("secret", "secret", "Default secret for use during testing")
	consumer    = flag.String("consumer", "consumer", "Def consumer")
	httpAddress = flag.String("http", "localhost:8086/lti", "Listen to")
)

// formatRequest generates ascii representation of a request
func formatRequest(r *http.Request) string {
    // Create return string
    var request []string // Add the request string
    url := fmt.Sprintf("%v %v %v", r.Method, r.URL, r.Proto)
    request = append(request, url) // Add the host
    request = append(request, fmt.Sprintf("Host: %v", r.Host)) // Loop through headers
    for name, headers := range r.Header {
    name = strings.ToLower(name)
    for _, h := range headers {
        request = append(request, fmt.Sprintf("%v: %v", name, h))
    }
    }
    
    // If this is a POST, add post data
    if r.Method == "POST" {
        r.ParseForm()
        request = append(request, "\n")
        request = append(request, r.Form.Encode())
    }   // Return the request as a string
    return strings.Join(request, "\n")
}

type MockAPIServer struct {
	Lessons     []*pb.Lesson
	Collections []*pb.Collection
	Lti         *pb.LtiMes
}

func (apiServer *MockAPIServer) StartAPI(config *config.SyringeConfig) error {
    
    flag.Parse()

	grpcPort := config.GRPCPort
	httpPort := config.HTTPPort

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
	if err != nil {
		log.Errorf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterLiveLessonsServiceServer(grpcServer, apiServer)
	// pb.RegisterCurriculumServiceServer(grpcServer, apiServer)
	pb.RegisterCollectionServiceServer(grpcServer, apiServer)
	pb.RegisterLessonServiceServer(grpcServer, apiServer)
    
    pb.RegisterLtiServiceServer(grpcServer, apiServer)
	// pb.RegisterSyringeInfoServiceServer(grpcServer, apiServer)
	// pb.RegisterKubeLabServiceServer(grpcServer, apiServer)
	defer grpcServer.Stop()

	// Start grpc server
	go grpcServer.Serve(lis)

	// Start REST proxy
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	gwmux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithInsecure()}

	// Register GRPC-gateway (HTTP) endpoints
	err = gw.RegisterLiveLessonsServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	if err != nil {
		return err
	}
	err = gw.RegisterLessonServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	if err != nil {
		return err
	}
	// err = gw.RegisterSyringeInfoServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	// if err != nil {
	// 	return err
	// }
	err = gw.RegisterCollectionServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	if err != nil {
		return err
	}
	
	err = gw.RegisterLtiServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	if err != nil {
		return err
	}
	// err = gw.RegisterCurriculumServiceHandlerFromEndpoint(ctx, gwmux, fmt.Sprintf(":%d", grpcPort), opts)
	// if err != nil {
	// 	return err
	// }

	// Handle swagger requests
	mux := http.NewServeMux()
	mux.Handle("/", gwmux)
	mux.HandleFunc("/livelesson.json", func(w http.ResponseWriter, req *http.Request) {
		io.Copy(w, strings.NewReader(swag.Livelesson))
	})
	mux.HandleFunc("/lesson.json", func(w http.ResponseWriter, req *http.Request) {
		io.Copy(w, strings.NewReader(swag.Lesson))
	})
	// mux.HandleFunc("/syringeinfo.json", func(w http.ResponseWriter, req *http.Request) {
	// 	io.Copy(w, strings.NewReader(swag.Syringeinfo))
	// })
	mux.HandleFunc("/collection.json", func(w http.ResponseWriter, req *http.Request) {
		io.Copy(w, strings.NewReader(swag.Collection))
	})
	// mux.HandleFunc("/curriculum.json", func(w http.ResponseWriter, req *http.Request) {
	// 	io.Copy(w, strings.NewReader(swag.Curriculum))
	// })
	serveSwagger(mux)

	log.WithFields(log.Fields{
		"gRPC Port": grpcPort,
		"HTTP Port": httpPort,
	}).Info("Syringe API starting...")

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: apiServer.grpcHandlerFunc(grpcServer, mux),
	}
	srv.ListenAndServe()
	return nil
}

// grpcHandlerFunc returns an http.Handler that delegates to grpcServer on incoming gRPC
// connections or otherHandler otherwise. Copied from cockroachdb.
func (apiServer *MockAPIServer) grpcHandlerFunc(grpcServer *grpc.Server, otherHandler http.Handler) http.Handler {

	// Add handler for grpc server
	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        p := lti.NewProvider(*secret, "http://localhost:8086/lti/")
        p.ConsumerKey = *consumer
        
//         log.Printf(formatRequest(r))
        if r.Method == "POST" {
            ok, err := p.IsValid(r)
            if ok == false {
                log.Printf("Invalid LTI request...")
            }
            if err != nil {
                log.Printf("Invalid LTI request")
                log.Println(err)
    //             return
            }

            if ok == true {
                log.Printf("Request OK")
                apiServer.Lti.Name = p.Get("lis_person_name_full")
                http.Redirect(w, r, "http://localhost:8080/lti", 302)
                
            }
        }

		// Temporary hack to get HTTP presentations working in antidote-web when working off
		// of this mocked API. Should figure out a way to specify path as a normal function of the API
		// rather than have antidote-web determine this on its own. (TODO)
		if strings.Contains(r.RequestURI, "webserver1") {
			http.Redirect(w, r, "http://127.0.0.1:8090/", 302)
		}

		// TODO(tamird): point to merged gRPC code rather than a PR.
		// This is a partial recreation of gRPC's internal checks https://github.com/grpc/grpc-go/pull/514/files#diff-95e9a25b738459a2d3030e1e6fa2a718R61
		if r.ProtoMajor == 2 && strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
		} else {
			otherHandler.ServeHTTP(w, r)
		}
	})

	// Allow CORS (ONLY IN PREPROD)
	// Add gorilla's logging handler for standards-based access logging
	return ghandlers.LoggingHandler(os.Stdout, allowCORS(handlerFunc))
}

// allowCORS allows Cross Origin Resoruce Sharing from any origin.
// Don't do this without consideration in production systems.
func allowCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if r.Method == "OPTIONS" && r.Header.Get("Access-Control-Request-Method") != "" {
				preflightHandler(w, r)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// preflightHandler adds the necessary headers in order to serve
// CORS from any origin using the methods "GET", "HEAD", "POST", "PUT", "DELETE"
// We insist, don't do this without consideration in production systems.
func preflightHandler(w http.ResponseWriter, r *http.Request) {
	headers := []string{"Content-Type", "Accept"}
	w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ","))
	methods := []string{"GET", "HEAD", "POST", "PUT", "DELETE"}
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ","))
	glog.Infof("preflight request for %s", r.URL.Path)
}

func serveSwagger(mux *http.ServeMux) {
	mime.AddExtensionType(".svg", "image/svg+xml")

	// Expose files in third_party/swagger-ui/ on <host>/swagger
	fileServer := http.FileServer(&assetfs.AssetFS{
		Asset:    swagger.Asset,
		AssetDir: swagger.AssetDir,
		Prefix:   "third_party/swagger-ui",
	})
	prefix := "/swagger/"
	mux.Handle(prefix, http.StripPrefix(prefix, fileServer))
}
