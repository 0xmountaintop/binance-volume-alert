package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type CoinGeckoResponse struct {
	Symbol string `json:"symbol"`
}

type BinanceKline []interface{}

type VolumeData struct {
	PrevVolume float64
	CurrVolume float64
	Ratio      float64
}

func getMarketCapRank() ([]string, error) {
	url := "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=100&page=1&sparkline=false"

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get market cap rank: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var coins []CoinGeckoResponse
	if err := json.Unmarshal(body, &coins); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	var symbols []string
	for _, coin := range coins {
		symbol := fmt.Sprintf("%sUSDT", strings.ToUpper(coin.Symbol))
		symbols = append(symbols, symbol)
	}

	return symbols, nil
}

func getBinanceVolume(symbol string) (*VolumeData, error) {
	url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=1h&limit=2", symbol)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get kline data: %v", err)
	}
	defer resp.Body.Close()

	// Handle invalid trading pairs
	if resp.StatusCode == 400 {
		return nil, nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var klines []BinanceKline
	if err := json.Unmarshal(body, &klines); err != nil {
		return nil, fmt.Errorf("failed to unmarshal klines: %v", err)
	}

	if len(klines) < 2 {
		return nil, fmt.Errorf("insufficient kline data")
	}

	prevVolume, _ := strconv.ParseFloat(klines[0][5].(string), 64)
	currVolume, _ := strconv.ParseFloat(klines[1][5].(string), 64)

	if prevVolume == 0 {
		return nil, nil
	}

	ratio := currVolume / prevVolume

	return &VolumeData{
		PrevVolume: prevVolume,
		CurrVolume: currVolume,
		Ratio:      ratio,
	}, nil
}

func alert(symbol string, data *VolumeData) {
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("⚠️ Volume Alert for %s\n", symbol)
	fmt.Printf("Previous Hour Volume: %.2f\n", data.PrevVolume)
	fmt.Printf("Current Hour Volume: %.2f\n", data.CurrVolume)
	fmt.Printf("Volume Ratio: %.2fx\n", data.Ratio)
	fmt.Printf("Time: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("=", 50))
}

func main() {
	fmt.Println("Starting volume monitor...")

	for {
		symbols, err := getMarketCapRank()
		if err != nil {
			fmt.Printf("Error getting market cap rank: %v\n", err)
			time.Sleep(5 * time.Minute)
			continue
		}

		for _, symbol := range symbols {
			volumeData, err := getBinanceVolume(symbol)
			if err != nil {
				fmt.Printf("Error getting volume data for %s: %v\n", symbol, err)
				continue
			}

			if volumeData != nil && volumeData.Ratio > 5 {
				alert(symbol, volumeData)
			}

			// Rate limiting
			time.Sleep(100 * time.Millisecond)
		}

		fmt.Printf("Check completed at %s\n", time.Now().Format("2006-01-02 15:04:05"))
		time.Sleep(5 * time.Minute)
	}
}
