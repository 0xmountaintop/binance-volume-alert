package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
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

var (
	bot              *tgbotapi.BotAPI
	monitoringStatus sync.Map
)

const (
	statusFile = "monitoring_status.json"
)

func init() {
	var err error

	if err = godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is not set")
	}

	bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)
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

func sendAlert(chatID int64, symbol string, data *VolumeData) {
	message := fmt.Sprintf("⚠️ Volume Alert for %s\n"+
		"Previous Hour Volume: %.2f\n"+
		"Current Hour Volume: %.2f\n"+
		"Volume Ratio: %.2fx\n"+
		"Time: %s",
		symbol,
		data.PrevVolume,
		data.CurrVolume,
		data.Ratio,
		time.Now().Format("2006-01-02 15:04:05"))

	msg := tgbotapi.NewMessage(chatID, message)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending alert: %v", err)
	}
}

func saveMonitoringStatus() {
	statusMap := make(map[int64]bool)

	monitoringStatus.Range(func(key, value interface{}) bool {
		chatID := key.(int64)
		status := value.(bool)
		statusMap[chatID] = status
		return true
	})

	data, err := json.Marshal(statusMap)
	if err != nil {
		log.Printf("Error marshaling monitoring status: %v", err)
		return
	}

	err = ioutil.WriteFile(statusFile, data, 0644)
	if err != nil {
		log.Printf("Error saving monitoring status: %v", err)
	}
}

func loadMonitoringStatus() {
	data, err := ioutil.ReadFile(statusFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error reading monitoring status file: %v", err)
		}
		return
	}

	statusMap := make(map[int64]bool)
	err = json.Unmarshal(data, &statusMap)
	if err != nil {
		log.Printf("Error unmarshaling monitoring status: %v", err)
		return
	}

	for chatID, status := range statusMap {
		monitoringStatus.Store(chatID, status)
		if status {
			go startMonitoring(chatID)
		}
	}
}

func startMonitoring(chatID int64) {
	monitoringStatus.Store(chatID, true)
	saveMonitoringStatus()
	msg := tgbotapi.NewMessage(chatID, "Volume monitoring started! You will receive alerts when volume increases more than 5x.")
	bot.Send(msg)

	for {
		monitoring, _ := monitoringStatus.Load(chatID)
		if !monitoring.(bool) {
			return
		}

		symbols, err := getMarketCapRank()
		if err != nil {
			log.Printf("Error getting market cap rank: %v\n", err)
			time.Sleep(5 * time.Minute)
			continue
		}

		for _, symbol := range symbols {
			monitoring, _ := monitoringStatus.Load(chatID)
			if !monitoring.(bool) {
				return
			}

			volumeData, err := getBinanceVolume(symbol)
			if err != nil {
				log.Printf("Error getting volume data for %s: %v\n", symbol, err)
				continue
			}

			if volumeData != nil && volumeData.Ratio > 5 {
				sendAlert(chatID, symbol, volumeData)
			}

			time.Sleep(100 * time.Millisecond)
		}

		log.Printf("Check completed for chat %d at %s\n", chatID, time.Now().Format("2006-01-02 15:04:05"))
		time.Sleep(5 * time.Minute)
	}
}

func stopMonitoring(chatID int64) {
	monitoringStatus.Store(chatID, false)
	saveMonitoringStatus()
	msg := tgbotapi.NewMessage(chatID, "Volume monitoring stopped!")
	bot.Send(msg)
}

func handleCommands() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID

		if !update.Message.IsCommand() {
			continue
		}

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(chatID,
				"Welcome to Binance Volume Monitor Bot!\n\n"+
					"Available commands:\n"+
					"/monitor - Start volume monitoring\n"+
					"/stop - Stop volume monitoring\n"+
					"/status - Check monitoring status")
			bot.Send(msg)

		case "monitor":
			monitoring, _ := monitoringStatus.Load(chatID)
			isMonitoring := monitoring != nil && monitoring.(bool)
			if !isMonitoring {
				go startMonitoring(chatID)
			} else {
				msg := tgbotapi.NewMessage(chatID, "Monitoring is already running!")
				bot.Send(msg)
			}

		case "stop":
			monitoring, _ := monitoringStatus.Load(chatID)
			isMonitoring := monitoring != nil && monitoring.(bool)
			if isMonitoring {
				stopMonitoring(chatID)
			} else {
				msg := tgbotapi.NewMessage(chatID, "Monitoring is not running!")
				bot.Send(msg)
			}

		case "status":
			status := "stopped"
			monitoring, _ := monitoringStatus.Load(chatID)
			if monitoring != nil && monitoring.(bool) {
				status = "running"
			}
			msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Monitoring is currently %s", status))
			bot.Send(msg)
		}
	}
}

func main() {
	log.Println("Starting Binance Volume Monitor Bot...")
	loadMonitoringStatus()
	handleCommands()
}
