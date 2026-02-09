package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "service-b"

var tracer trace.Tracer

type CEPRequest struct {
	CEP string `json:"cep"`
}

type WeatherResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

type ErrorResponse struct {
	Message string `json:"message"`
}

// ViaCEP API response
type ViaCEPResponse struct {
	CEP         string `json:"cep"`
	Logradouro  string `json:"logradouro"`
	Complemento string `json:"complemento"`
	Bairro      string `json:"bairro"`
	Localidade  string `json:"localidade"`
	UF          string `json:"uf"`
	Erro        string `json:"erro"`
}

// WeatherAPI response
type WeatherAPIResponse struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
}

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()

	otelCollectorURL := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelCollectorURL == "" {
		otelCollectorURL = "otel-collector:4317"
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otelCollectorURL),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracer = tp.Tracer(serviceName)

	return tp, nil
}

func validateCEP(cep string) bool {
	matched, _ := regexp.MatchString(`^\d{8}$`, cep)
	return matched
}

func lookupCEP(ctx context.Context, cep string) (*ViaCEPResponse, error) {
	ctx, span := tracer.Start(ctx, "lookup-cep-viacep")
	defer span.End()

	span.SetAttributes(attribute.String("cep", cep))

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep), nil)
	if err != nil {
		span.SetAttributes(attribute.String("error", "failed to create request"))
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		span.SetAttributes(attribute.String("error", "viacep request failed"))
		return nil, err
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http_status", resp.StatusCode))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.SetAttributes(attribute.String("error", "failed to read response"))
		return nil, err
	}

	var viaCEP ViaCEPResponse
	if err := json.Unmarshal(body, &viaCEP); err != nil {
		span.SetAttributes(attribute.String("error", "failed to parse response"))
		return nil, err
	}

	if viaCEP.Erro == "true" || viaCEP.Localidade == "" {
		span.SetAttributes(attribute.Bool("cep_found", false))
		return nil, nil
	}

	span.SetAttributes(
		attribute.Bool("cep_found", true),
		attribute.String("city", viaCEP.Localidade),
	)

	return &viaCEP, nil
}

func getWeather(ctx context.Context, city string) (float64, error) {
	ctx, span := tracer.Start(ctx, "get-weather-api")
	defer span.End()

	span.SetAttributes(attribute.String("city", city))

	apiKey := os.Getenv("WEATHER_API_KEY")
	if apiKey == "" {
		span.SetAttributes(attribute.String("error", "missing api key"))
		return 0, fmt.Errorf("WEATHER_API_KEY not set")
	}

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	encodedCity := url.QueryEscape(city)
	apiURL := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s&aqi=no", apiKey, encodedCity)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		span.SetAttributes(attribute.String("error", "failed to create request"))
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		span.SetAttributes(attribute.String("error", "weather api request failed"))
		return 0, err
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http_status", resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		span.SetAttributes(attribute.String("error", "weather api returned non-200"))
		return 0, fmt.Errorf("weather API returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.SetAttributes(attribute.String("error", "failed to read response"))
		return 0, err
	}

	var weatherResp WeatherAPIResponse
	if err := json.Unmarshal(body, &weatherResp); err != nil {
		span.SetAttributes(attribute.String("error", "failed to parse response"))
		return 0, err
	}

	span.SetAttributes(attribute.Float64("temp_c", weatherResp.Current.TempC))

	return weatherResp.Current.TempC, nil
}

func celsiusToFahrenheit(c float64) float64 {
	return c*1.8 + 32
}

func celsiusToKelvin(c float64) float64 {
	return c + 273
}

func handleWeather(w http.ResponseWriter, r *http.Request) {
	// Extract context from incoming request (with propagated trace)
	ctx := r.Context()
	ctx, span := tracer.Start(ctx, "handle-weather-request")
	defer span.End()

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req CEPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.SetAttributes(attribute.String("error", "invalid json"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(ErrorResponse{Message: "invalid zipcode"})
		return
	}

	span.SetAttributes(attribute.String("cep", req.CEP))

	// Validate CEP format
	if !validateCEP(req.CEP) {
		span.SetAttributes(attribute.String("error", "invalid cep format"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(ErrorResponse{Message: "invalid zipcode"})
		return
	}

	// Lookup CEP
	viaCEP, err := lookupCEP(ctx, req.CEP)
	if err != nil {
		span.SetAttributes(attribute.String("error", "cep lookup failed"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Message: "internal error"})
		return
	}

	if viaCEP == nil {
		span.SetAttributes(attribute.String("error", "cep not found"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Message: "can not find zipcode"})
		return
	}

	// Get weather
	tempC, err := getWeather(ctx, viaCEP.Localidade)
	if err != nil {
		span.SetAttributes(attribute.String("error", "weather lookup failed"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Message: "failed to get weather"})
		return
	}

	// Calculate temperatures
	tempF := celsiusToFahrenheit(tempC)
	tempK := celsiusToKelvin(tempC)

	span.SetAttributes(
		attribute.String("city", viaCEP.Localidade),
		attribute.Float64("temp_c", tempC),
		attribute.Float64("temp_f", tempF),
		attribute.Float64("temp_k", tempK),
	)

	response := WeatherResponse{
		City:  viaCEP.Localidade,
		TempC: tempC,
		TempF: tempF,
		TempK: tempK,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func main() {
	tp, err := initTracer()
	if err != nil {
		log.Fatalf("failed to initialize tracer: %v", err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/weather", handleWeather)

	handler := otelhttp.NewHandler(mux, "service-b-server")

	server := &http.Server{
		Addr:    ":8081",
		Handler: handler,
	}

	go func() {
		log.Println("Service B starting on port 8081")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Service B stopped")
}
