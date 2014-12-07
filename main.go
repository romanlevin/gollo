package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	mw, err := getMultiWeatherProvider("conf.json")
	if err != nil {
		log.Fatal(err)
		return
	}
	http.HandleFunc("/weather/", func(w http.ResponseWriter, r *http.Request) {
		begin := time.Now()
		city := strings.SplitN(r.URL.Path, "/", 3)[2]

		temp, err := mw.temperature(city)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"city": city,
			"temp": temp,
			"took": time.Since(begin).String(),
		})
	})
	http.ListenAndServe(":8080", nil)
}

func getMultiWeatherProvider(confFile string) (mw multiWeatherProvider, err error) {
	var conf struct {
		WeatherUnderground struct {
			ApiKey string
		}
		ForecastIo struct {
			ApiKey string
		}
	}
	file, err := os.Open(confFile)
	if err != nil {
		return nil, err
	}
	if err := json.NewDecoder(file).Decode(&conf); err != nil {
		return nil, err
	}
	mw = multiWeatherProvider{
		openWeatherMap{},
		weatherUnderground{apiKey: conf.WeatherUnderground.ApiKey},
		forecastIo{apiKey: conf.ForecastIo.ApiKey},
	}
	return
}

type openWeatherMap struct{}

func (w openWeatherMap) temperature(city string) (float64, error) {
	begin := time.Now()
	resp, err := http.Get("http://api.openweathermap.org/data/2.5/weather?q=" + city)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	var d struct {
		Main struct {
			Kelvin float64 `json:"temp"`
		} `json:"main"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return 0, err
	}

	log.Printf("openWeatherMap: %s: %.2f; took %s", city, d.Main.Kelvin, time.Since(begin).String())
	return d.Main.Kelvin, nil
}

type weatherUnderground struct {
	apiKey string
}

func (w weatherUnderground) temperature(city string) (float64, error) {
	begin := time.Now()
	resp, err := http.Get("http://api.wunderground.com/api/" + w.apiKey + "/conditions/q/" + city + ".json")
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	var d struct {
		Observation struct {
			Celsius float64 `json:"temp_c"`
		} `json:"current_observation"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return 0, err
	}

	kelvin := d.Observation.Celsius + 273.15
	log.Printf("weatherUnderground: %s: %.2f; took %s", city, kelvin, time.Since(begin).String())
	return kelvin, nil
}

type weatherProvider interface {
	temperature(city string) (float64, error) // In Kelvin!
}

type forecastIo struct {
	apiKey string
}

func (w forecastIo) temperature(city string) (float64, error) {
	begin := time.Now()

	locationResp, err := http.Get("https://maps.googleapis.com/maps/api/geocode/json?address=" + city)
	if err != nil {
		return 0, err
	}
	defer locationResp.Body.Close()

	var location struct {
		Results []struct {
			Geometry struct {
				Location struct {
					Latitude  float64 `json:"lat"`
					Longitude float64 `json:"lng"`
				} `json:"location"`
			} `json:"geometry"`
		} `json:"results"`
	}

	if err := json.NewDecoder(locationResp.Body).Decode(&location); err != nil {
		return 0, err
	}

	latitude, longitude := strconv.FormatFloat(location.Results[0].Geometry.Location.Latitude, 'f', -1, 64), strconv.FormatFloat(location.Results[0].Geometry.Location.Longitude, 'f', -1, 64)
	resp, err := http.Get("https://api.forecast.io/forecast/" + w.apiKey + "/" + latitude + "," + longitude + "?units=si")
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	var d struct {
		Currently struct {
			Temperature float64 `json:"temperature"`
		} `json:"currently"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return 0, err
	}

	kelvin := d.Currently.Temperature + 273.15
	log.Printf("forecast.io: %s: %.2f; took %s", city, kelvin, time.Since(begin).String())
	return kelvin, nil
}

type multiWeatherProvider []weatherProvider

func (w multiWeatherProvider) temperature(city string) (float64, error) {
	temps := make(chan float64, len(w))
	errs := make(chan error, len(w))

	for _, provider := range w {
		go func(p weatherProvider) {
			k, err := p.temperature(city)
			if err != nil {
				errs <- err
				return
			}
			temps <- k
		}(provider)
	}

	sum := 0.0

	for i := 0; i < len(w); i++ {
		select {
		case temp := <-temps:
			sum += temp
		case err := <-errs:
			return 0, err
		case <-time.After(time.Millisecond * 1500):
			log.Printf("%s timed out", w)
		}
	}

	return sum / float64(len(w)), nil
}
