package server

import (
 "bytes"
 "log"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestRequestTimingKeepsStreamingAndRedactsQuery(t *testing.T) {
 var logs bytes.Buffer
 previous:=log.Writer();log.SetOutput(&logs);defer log.SetOutput(previous)
 mux:=http.NewServeMux()
 mux.HandleFunc("GET /stream/{id}",func(w http.ResponseWriter,r *http.Request){
  if _,ok:=w.(http.Flusher);!ok {t.Fatal("stream flushing lost")}
  _,_=w.Write([]byte("event"));w.(http.Flusher).Flush()
 })
 out:=httptest.NewRecorder()
 requestTiming(mux).ServeHTTP(out,httptest.NewRequest("GET","/stream/private-value?token=private-token",nil))
 if !out.Flushed||out.Code!=200 {t.Fatal("stream response changed")}
 if !strings.Contains(logs.String(),"duration_ms=")||!strings.Contains(logs.String(),"GET /stream/{id}") {t.Fatal("missing timing/route")}
 for _,secret:=range []string{"private-value","private-token"} {if strings.Contains(logs.String(),secret){t.Fatal("sensitive request field logged")}}
 if out.Header().Get("X-Request-ID")=="" {t.Fatal("missing request id")}
}
