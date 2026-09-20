//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"rinotravel-api/internal/app"
	"rinotravel-api/internal/document/s3storage"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/server"
)

type api struct {
	t       *testing.T
	handler http.Handler
	token   string
}

func (a *api) do(method, path, body string) (int, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (a *api) must(method, path, body string, want int) map[string]any {
	a.t.Helper()
	code, out := a.do(method, path, body)
	if code != want {
		a.t.Fatalf("%s %s = %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func newApp(t *testing.T) *api {
	t.Helper()
	ctx := context.Background()
	cfg := config.Config{
		Env: config.Development, AuthRateLimit: 100000, RegistrationCode: "dev-registration-code",
		S3Bucket: "rinotravel-it-" + ids.New()[24:], S3Region: "us-east-1", S3Endpoint: "http://localhost:9000",
		S3AccessKeyID: "minioadmin", S3SecretAccessKey: "minioadmin",
	}
	storage, err := s3storage.New(ctx, s3storage.Config{Bucket: cfg.S3Bucket, Region: cfg.S3Region, Endpoint: cfg.S3Endpoint, AccessKeyID: cfg.S3AccessKeyID, SecretAccessKey: cfg.S3SecretAccessKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Client().CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.S3Bucket)}); err != nil {
		t.Skipf("MinIO is not reachable on localhost:9000 (docker compose up -d minio): %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	modules, err := app.Build(ctx, app.Deps{Logger: logger, Config: cfg, DB: mongotest.Database(t)})
	if err != nil {
		t.Fatal(err)
	}
	return &api{t: t, handler: server.NewHandler(server.Options{Logger: logger, Modules: modules})}
}

func TestWholeApplicationAgainstRealInfrastructure(t *testing.T) {
	a := newApp(t)
	reg := a.must("POST", "/api/v1/auth/register", `{"email":"ana@example.com","name":"Ana","password":"correct horse battery","registrationCode":"dev-registration-code"}`, 201)
	a.token = reg["token"].(string)

	tripID := a.must("POST", "/api/v1/trips", `{"name":"Japan","destination":"Tokyo","startDate":"2027-04-01","endDate":"2027-04-15","timezone":"Asia/Tokyo","currency":"JPY"}`, 201)["id"].(string)
	base := "/api/v1/trips/" + tripID

	day := a.must("POST", base+"/itinerary-days", `{"date":"2027-04-03","title":"Asakusa"}`, 201)
	a.must("POST", base+"/itinerary-days", `{"date":"2027-04-03"}`, 409)
	place := a.must("POST", base+"/places", `{"name":"Senso-ji","category":"ATTRACTION","location":{"name":"Senso-ji","latitude":35.7148,"longitude":139.7967},"estimatedCost":{"amount":500,"currency":"JPY"}}`, 201)
	item := a.must("POST", base+"/itinerary-items/from-place", fmt.Sprintf(`{"placeId":%q,"dayId":%q,"start":{"dateTime":"2027-04-03T09:00"}}`, place["id"], day["id"]), 201)
	if item["title"] != "Senso-ji" || item["estimatedCost"].(map[string]any)["amount"] != float64(500) || item["location"].(map[string]any)["latitude"] != 35.7148 {
		t.Errorf("place round trip through MongoDB: %v", item)
	}
	a.must("POST", base+"/restaurants", `{"name":"Sushi Dai","status":"RESERVED","reservationAt":{"dateTime":"2027-04-03T19:00"},"reservationCode":"C1","desiredDishes":["omakase"]}`, 201)
	a.must("POST", base+"/flights", `{"flightNumber":"LA8084","departureAirport":"GRU","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-01T22:00","timezone":"America/Sao_Paulo"},"arrival":{"dateTime":"2027-04-02T18:30","timezone":"Europe/Lisbon"}}`, 201)
	a.must("POST", base+"/hotels", `{"name":"Park Hyatt","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"}}`, 201)
	transfer := a.must("POST", base+"/transfers", `{"legs":[{"mode":"TRAIN","origin":{"name":"A"},"destination":{"name":"B"},"departure":{"dateTime":"2027-04-03T08:00"},"arrival":{"dateTime":"2027-04-03T08:40"},"cost":{"amount":300,"currency":"JPY"}}]}`, 201)
	if transfer["durationMinutes"] != float64(40) || transfer["totalCost"].(map[string]any)["amount"] != float64(300) {
		t.Errorf("transfer round trip: %v", transfer)
	}

	timeline := a.must("GET", base+"/itinerary", "", 200)["days"].([]any)
	if len(timeline) < 4 {
		t.Errorf("timeline has %d days, want the trip's day plus flight, hotel and transfer dates: %v", len(timeline), timeline)
	}

	t.Run("sync uses real transactions and a durable mutation log", func(t *testing.T) {
		newID, m1, m2 := ids.New(), ids.New(), ids.New()
		push := func(mutation string) map[string]any {
			return a.must("POST", base+"/sync", `{"mutations":[`+mutation+`]}`, 200)["results"].([]any)[0].(map[string]any)
		}
		create := fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"CREATE","payload":{"dayId":%q,"title":"Offline","category":"OTHER"}}`, m1, newID, day["id"])

		if r := push(create); r["status"] != "applied" {
			t.Fatalf("first push = %v", r)
		}
		if r := push(create); r["status"] != "duplicate" {
			t.Errorf("retry = %v, want a duplicate answered from the log", r)
		}
		if _, out := a.do("GET", base+"/itinerary-items/"+newID, ""); out["title"] != "Offline" {
			t.Errorf("stored item = %v", out)
		}
		stale := push(fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"UPDATE","baseVersion":9,"payload":{"title":"x"}}`, m2, newID))
		if stale["status"] != "conflict" || stale["record"] == nil {
			t.Errorf("stale update = %v", stale)
		}

		pull := a.must("GET", base+"/sync", "", 200)
		seen := map[string]bool{}
		for _, c := range pull["changes"].([]any) {
			seen[c.(map[string]any)["entity"].(string)] = true
		}
		for _, entity := range []string{"trip", "itinerary_day", "itinerary_item", "place", "restaurant", "flight", "hotel", "transfer"} {
			if !seen[entity] {
				t.Errorf("the initial sync is missing %s (got %v)", entity, seen)
			}
		}
	})

	t.Run("documents travel through signed URLs to the real store", func(t *testing.T) {
		content := []byte("%PDF-1.4 fake ticket content")
		sum := sha256.Sum256(content)
		checksum := hex.EncodeToString(sum[:])
		init := a.must("POST", base+"/documents", fmt.Sprintf(`{"name":"Ticket","type":"TICKET","fileName":"ticket.pdf","mimeType":"application/pdf","size":%d,"checksum":%q}`, len(content), checksum), 201)
		id := init["document"].(map[string]any)["id"].(string)
		upload := init["upload"].(map[string]any)

		put := func(data []byte, checksumHeader string) int {
			req, _ := http.NewRequest(upload["method"].(string), upload["url"].(string), bytes.NewReader(data))
			for k, v := range upload["headers"].(map[string]any) {
				req.Header.Set(k, v.(string))
			}
			if checksumHeader != "" {
				req.Header.Set("X-Amz-Checksum-Sha256", checksumHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			return resp.StatusCode
		}

		a.must("POST", base+"/documents/"+id+"/complete", "", 422)
		if code := put(content, ""); code != 200 {
			t.Fatalf("upload to storage = %d", code)
		}
		done := a.must("POST", base+"/documents/"+id+"/complete", "", 200)
		if done["status"] != "READY" || done["checksum"] != checksum {
			t.Errorf("complete = %v", done)
		}

		dl := a.must("GET", base+"/documents/"+id+"/download", "", 200)["download"].(map[string]any)
		resp, err := http.Get(dl["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(got, content) || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename="ticket.pdf"`) {
			t.Errorf("download = %q, disposition %q", got, resp.Header.Get("Content-Disposition"))
		}

		bad := a.must("POST", base+"/documents", fmt.Sprintf(`{"name":"Tampered","type":"OTHER","fileName":"x.pdf","mimeType":"application/pdf","size":%d,"checksum":%q}`, len(content), checksum), 201)
		upload = bad["upload"].(map[string]any)
		wrong := sha256.Sum256([]byte("something else"))
		if code := put(content, hex2b64(wrong[:])); code == 200 {
			t.Error("the storage accepted content that does not match the signed checksum")
		}

		a.must("DELETE", base+"/documents/"+id, "", 204)
		time.Sleep(200 * time.Millisecond)
		if resp2, err := http.Get(dl["url"].(string)); err == nil {
			resp2.Body.Close()
			if resp2.StatusCode == 200 {
				t.Error("the object must be removed from storage after the document is deleted")
			}
		}
	})
}

func hex2b64(b []byte) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		v := uint(chunk[0])<<16 | uint(chunk[1])<<8 | uint(chunk[2])
		out.WriteByte(table[v>>18&63])
		out.WriteByte(table[v>>12&63])
		if n > 1 {
			out.WriteByte(table[v>>6&63])
		} else {
			out.WriteByte('=')
		}
		if n > 2 {
			out.WriteByte(table[v&63])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}
