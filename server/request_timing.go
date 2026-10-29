package server

import (
 "bufio"
 "fmt"
 "log"
 "net"
 "net/http"
 "regexp"
 "time"
 "github.com/google/uuid"
)

type timingWriter struct { http.ResponseWriter; status int }
func (w *timingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *timingWriter) WriteHeader(status int) {
 if status >= 200 { if w.status != 0 { return }; w.status=status }
 w.ResponseWriter.WriteHeader(status)
}
func (w *timingWriter) Write(p []byte) (int,error) { if w.status==0 {w.WriteHeader(200)}; return w.ResponseWriter.Write(p) }
func (w *timingWriter) Flush() { if w.status==0 {w.WriteHeader(200)}; _=http.NewResponseController(w.ResponseWriter).Flush() }
func (w *timingWriter) Hijack() (net.Conn,*bufio.ReadWriter,error) { return http.NewResponseController(w.ResponseWriter).Hijack() }
var safeRequestID = regexp.MustCompile(`^[a-fA-F0-9-]{32,36}$`)

func requestTiming(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  started:=time.Now(); id:=r.Header.Get("X-Request-ID")
  if !safeRequestID.MatchString(id) {id=uuid.NewString()}
  w.Header().Set("X-Request-ID",id)
  out:=&timingWriter{ResponseWriter:w}
  defer func(){
   status:=out.status; if status==0 {status=200}
   // 仅使用路由模板，禁止将查询令牌、原始路径及正文写入日志。
   route:=r.Pattern; if route=="" {route="unmatched"}
   log.Print(fmt.Sprintf("[http] request_id=%s route=%q status=%d duration_ms=%d",id,route,status,time.Since(started).Milliseconds()))
  }()
  next.ServeHTTP(out,r)
 })
}
