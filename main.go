package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"airbnb-scraper/config"
	"airbnb-scraper/db"
	"airbnb-scraper/filter"
	"airbnb-scraper/models"
	"airbnb-scraper/parser"
	"airbnb-scraper/scheduler"
	"airbnb-scraper/scraper"
	"airbnb-scraper/sheets"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	// Parse command line arguments
	url := flag.String("url", "", "Airbnb search URL (optional, if not provided, runs as Telegram bot)")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	maxPages := flag.Int("pages", 5, "Maximum number of pages to scrape")
	spreadsheetURL := flag.String("spreadsheet", "https://docs.google.com/spreadsheets/d/1FoGJ6ZzDIfFv3ZZ6_qWSn8hzEk4tlUEAT7ClQKYRmFo/edit?usp=sharing", "Google Sheets URL")
	credentialsPath := flag.String("credentials", "", "Path to Google service account credentials JSON file (or use GOOGLE_SHEETS_CREDENTIALS env var)")
	flag.Parse()

	// If URL is provided, run in CLI mode
	if *url != "" {
		runCLIMode(*url, *configPath, *maxPages, *spreadsheetURL, *credentialsPath)
		return
	}

	// Otherwise, run as Telegram bot
	runTelegramBot(*configPath, *maxPages, *spreadsheetURL, *credentialsPath)
}

// runCLIMode runs the scraper in CLI mode
func runCLIMode(urlStr, configPath string, maxPages int, spreadsheetURL, credentialsPath string) {
	// Add currency=USD to URL
	urlStr = addCurrencyToURL(urlStr)

	// Load configuration
	cfg := loadConfig(configPath)

	// Perform scraping
	filteredListings, allListings, err := scrapeListings(urlStr, maxPages, cfg)
	if err != nil {
		log.Fatalf("Scraping failed: %v\n", err)
	}

	// Display results to console
	fmt.Printf("Found %d listings before filtering\n", len(allListings))
	fmt.Printf("Found %d listings after filtering\n", len(filteredListings))
	fmt.Println("---")

	if len(filteredListings) == 0 {
		fmt.Println("No listings match the filter criteria.")
		return
	}

	fmt.Println("Filtered Listings:")
	fmt.Println("==================")
	formatListingsConsole(filteredListings)

	// Write to Google Sheets
	spreadsheetID := sheets.ExtractSpreadsheetID(spreadsheetURL)
	if spreadsheetID == "" {
		log.Printf("Warning: Could not extract spreadsheet ID from URL: %s\n", spreadsheetURL)
		return
	}

	writer, err := sheets.NewWriter(spreadsheetID, credentialsPath)
	if err != nil {
		log.Printf("Warning: Failed to initialize Google Sheets writer: %v\n", err)
		return
	}

	// Format filter information for CLI mode
	filterInfo := fmt.Sprintf("Min Reviews: %d, Min Price: %.2f, Max Price: %.2f, Min Stars: %.2f",
		cfg.Filters.MinReviews, cfg.Filters.MinPrice, cfg.Filters.MaxPrice, cfg.Filters.MinStars)

	// Create a temporary sheet name for CLI mode
	sheetName := fmt.Sprintf("CLI_%s", time.Now().Format("20060102_150405"))
	
	// Use CreateSheetAndWriteListings to insert at the beginning
	_, _, err = writer.CreateSheetAndWriteListings(sheetName, filteredListings, urlStr, filterInfo)
	if err != nil {
		log.Printf("Warning: Failed to write to Google Sheets: %v\n", err)
	} else {
		fmt.Printf("\nSuccessfully wrote %d listings to Google Sheets\n", len(filteredListings))
	}
}

// Allowed user IDs
var allowedUserIDs = map[int64]bool{
	420478432: true,
	425120436: true,
}

// handleCallbackQuery handles callback queries from inline keyboard buttons
func handleCallbackQuery(bot *tgbotapi.BotAPI, database *db.DB, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID
	data := callback.Data

	// Acknowledge callback
	bot.Send(tgbotapi.NewCallback(callback.ID, ""))

	// Handle different callback types
	if strings.HasPrefix(data, "config_") {
		configType := strings.TrimPrefix(data, "config_")
		handleConfigCallback(bot, database, chatID, userID, configType, callback.Message.MessageID)
	} else if strings.HasPrefix(data, "set_") {
		// Format: set_configType_value
		parts := strings.SplitN(data, "_", 3)
		if len(parts) == 3 {
			configType := parts[1]
			valueStr := parts[2]
			handleSetConfigValue(bot, database, chatID, userID, configType, valueStr, callback.Message.MessageID)
		}
	}
}

// handleConfigCallback shows options for changing a specific config value
func handleConfigCallback(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64, configType string, messageID int) {
	userConfig, err := database.GetUserConfig(userID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error loading config: %v", err)))
		return
	}

	var text string
	var keyboard tgbotapi.InlineKeyboardMarkup

	switch configType {
	case "max_pages":
		currentValue := userConfig.MaxPages
		text = fmt.Sprintf("📄 Max Pages\n\nCurrent: %d\n\nSelect new value:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("3", "set_max_pages_3"),
				tgbotapi.NewInlineKeyboardButtonData("5", "set_max_pages_5"),
				tgbotapi.NewInlineKeyboardButtonData("10", "set_max_pages_10"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("15", "set_max_pages_15"),
				tgbotapi.NewInlineKeyboardButtonData("20", "set_max_pages_20"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config_back"),
			),
		)
	case "min_reviews":
		currentValue := userConfig.MinReviews
		text = fmt.Sprintf("⭐ Min Reviews\n\nCurrent: %d\n\nSelect new value:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0", "set_min_reviews_0"),
				tgbotapi.NewInlineKeyboardButtonData("5", "set_min_reviews_5"),
				tgbotapi.NewInlineKeyboardButtonData("10", "set_min_reviews_10"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("20", "set_min_reviews_20"),
				tgbotapi.NewInlineKeyboardButtonData("50", "set_min_reviews_50"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config_back"),
			),
		)
	case "min_price":
		currentValue := userConfig.MinPrice
		text = fmt.Sprintf("💰 Min Price\n\nCurrent: %.2f\n\nSelect new value:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0", "set_min_price_0"),
				tgbotapi.NewInlineKeyboardButtonData("50", "set_min_price_50"),
				tgbotapi.NewInlineKeyboardButtonData("100", "set_min_price_100"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("200", "set_min_price_200"),
				tgbotapi.NewInlineKeyboardButtonData("500", "set_min_price_500"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config_back"),
			),
		)
	case "max_price":
		currentValue := userConfig.MaxPrice
		text = fmt.Sprintf("💰 Max Price\n\nCurrent: %.2f\n\nSelect new value:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("500", "set_max_price_500"),
				tgbotapi.NewInlineKeyboardButtonData("1000", "set_max_price_1000"),
				tgbotapi.NewInlineKeyboardButtonData("2000", "set_max_price_2000"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("3000", "set_max_price_3000"),
				tgbotapi.NewInlineKeyboardButtonData("5000", "set_max_price_5000"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config_back"),
			),
		)
	case "min_stars":
		currentValue := userConfig.MinStars
		text = fmt.Sprintf("⭐ Min Stars\n\nCurrent: %.2f\n\nSelect new value:", currentValue)
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("0.0", "set_min_stars_0"),
				tgbotapi.NewInlineKeyboardButtonData("3.0", "set_min_stars_3"),
				tgbotapi.NewInlineKeyboardButtonData("4.0", "set_min_stars_4"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("4.5", "set_min_stars_4.5"),
				tgbotapi.NewInlineKeyboardButtonData("4.8", "set_min_stars_4.8"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "config_back"),
			),
		)
	case "back":
		// Show main config menu
		userConfig, err := database.GetUserConfig(userID)
		if err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Error loading config: %v", err)))
			return
		}

		configText := fmt.Sprintf(
			"⚙️ Current Configuration:\n\n"+
				"📄 Max Pages: %d\n"+
				"⭐ Min Reviews: %d\n"+
				"💰 Min Price: %.2f\n"+
				"💰 Max Price: %.2f\n"+
				"⭐ Min Stars: %.2f\n\n"+
				"Click buttons below to change values:",
			userConfig.MaxPages, userConfig.MinReviews, userConfig.MinPrice, userConfig.MaxPrice, userConfig.MinStars)

		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", "config_max_pages"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", "config_min_reviews"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", "config_min_price"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", "config_max_price"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", "config_min_stars"),
			),
		)
		text = configText
	default:
		return
	}

	// Edit the message with new keyboard
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ReplyMarkup = &keyboard
	bot.Send(editMsg)
}

// handleSetConfigValue updates a config value and shows confirmation
func handleSetConfigValue(bot *tgbotapi.BotAPI, database *db.DB, chatID int64, userID int64, configType string, valueStr string, messageID int) {
	var err error
	var updateText string

	switch configType {
	case "max_pages":
		var value int
		if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, &value, nil, nil, nil, nil)
		updateText = fmt.Sprintf("✅ Max Pages updated to %d", value)
	case "min_reviews":
		var value int
		if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, &value, nil, nil, nil)
		updateText = fmt.Sprintf("✅ Min Reviews updated to %d", value)
	case "min_price":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, &value, nil, nil)
		updateText = fmt.Sprintf("✅ Min Price updated to %.2f", value)
	case "max_price":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, nil, &value, nil)
		updateText = fmt.Sprintf("✅ Max Price updated to %.2f", value)
	case "min_stars":
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
			bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", valueStr)))
			return
		}
		err = database.UpdateUserConfig(userID, nil, nil, nil, nil, &value)
		updateText = fmt.Sprintf("✅ Min Stars updated to %.2f", value)
	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Unknown config type"))
		return
	}

	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Error updating config: %v", err)))
		return
	}

	// Show updated config
	userConfig, err := database.GetUserConfig(userID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, updateText))
		return
	}

	configText := fmt.Sprintf(
		"%s\n\n⚙️ Current Configuration:\n\n"+
			"📄 Max Pages: %d\n"+
			"⭐ Min Reviews: %d\n"+
			"💰 Min Price: %.2f\n"+
			"💰 Max Price: %.2f\n"+
			"⭐ Min Stars: %.2f\n\n"+
			"Click buttons below to change values:",
		updateText, userConfig.MaxPages, userConfig.MinReviews, userConfig.MinPrice, userConfig.MaxPrice, userConfig.MinStars)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", "config_max_pages"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", "config_min_reviews"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", "config_min_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", "config_max_price"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", "config_min_stars"),
		),
	)

	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, configText)
	editMsg.ReplyMarkup = &keyboard
	bot.Send(editMsg)
}

// runTelegramBot runs the scraper as a Telegram bot
func runTelegramBot(configPath string, maxPages int, spreadsheetURL, credentialsPath string) {
	// Refresh environment variables (Windows-specific)
	refreshEnvVars()

	// Get bot token from environment
	botToken := os.Getenv("AIR_KEY_TG")
	if botToken == "" {
		log.Fatalf("Error: AIR_KEY_TG environment variable is not set")
	}

	// Initialize bot
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatalf("Failed to initialize bot: %v\n", err)
	}

	log.Printf("Authorized on account %s\n", bot.Self.UserName)

	// Send startup notification to admin (only once)
	adminID := int64(420478432)
	startupMsg := tgbotapi.NewMessage(adminID, "🚀 Service started successfully!")
	_, err = bot.Send(startupMsg)
	if err != nil {
		log.Printf("Warning: Failed to send startup notification to admin: %v\n", err)
	} else {
		log.Printf("Startup notification sent to admin %d\n", adminID)
	}

	// Initialize database
	database, err := db.NewDB()
	if err != nil {
		log.Fatalf("Error: Failed to initialize database: %v\n", err)
	}
	defer database.Close()
	log.Println("Database initialized successfully")

	// Initialize Google Sheets writer
	spreadsheetID := sheets.ExtractSpreadsheetID(spreadsheetURL)
	if spreadsheetID == "" {
		log.Fatalf("Error: Could not extract spreadsheet ID from URL: %s\n", spreadsheetURL)
	}

	// Check if credentials are available
	credsEnv := os.Getenv("GOOGLE_SHEETS_CREDENTIALS")
	if credentialsPath == "" && credsEnv == "" {
		log.Fatalf("Error: GOOGLE_SHEETS_CREDENTIALS environment variable is not set and no credentials file path provided")
	}
	if credentialsPath == "" && credsEnv != "" {
		log.Printf("Using GOOGLE_SHEETS_CREDENTIALS from environment variable (length: %d chars)\n", len(credsEnv))
	}

	writer, err := sheets.NewWriter(spreadsheetID, credentialsPath)
	if err != nil {
		log.Fatalf("Error: Failed to initialize Google Sheets writer: %v\n", err)
	}

	log.Printf("Google Sheets writer initialized for spreadsheet: %s\n", spreadsheetID)

	// Initialize and start scheduler (browser will be created on-demand)
	sched := scheduler.NewScheduler(database, bot, writer, spreadsheetURL)
	sched.Start()
	log.Println("Scheduler started (browser will be created on-demand for each request)")
	defer sched.Stop()

	// Set up update configuration - start from latest update to skip old ones
	// Get the latest update ID first to avoid processing old updates
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60
	updateConfig.Offset = -1 // This will get only new updates

	updates := bot.GetUpdatesChan(updateConfig)

	// Handle updates
	for update := range updates {
		// Handle callback queries (button presses)
		if update.CallbackQuery != nil {
			userID := update.CallbackQuery.From.ID
			if !allowedUserIDs[userID] {
				log.Printf("Unauthorized user attempted to use callback: %d\n", userID)
				bot.Send(tgbotapi.NewCallback(update.CallbackQuery.ID, "Sorry, you are not authorized."))
				continue
			}

			if update.CallbackQuery.Message != nil {
				handleCallbackQuery(bot, database, update.CallbackQuery)
			}
			continue
		}

		if update.Message == nil {
			continue
		}

		userID := update.Message.From.ID

		// Handle commands first (before authorization check for /start)
		if update.Message.IsCommand() {
			// Check authorization for commands (except /start which should work for everyone initially)
			command := update.Message.Command()
			if command != "start" && !allowedUserIDs[userID] {
				log.Printf("Unauthorized user attempted to use command: %d\n", userID)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Sorry, you are not authorized to use this bot.")
				bot.Send(msg)
				continue
			}

			switch command {
			case "start":
				// Check if user is allowed
				if !allowedUserIDs[userID] {
					log.Printf("Unauthorized user attempted to use bot: %d\n", userID)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Sorry, you are not authorized to use this bot.")
					bot.Send(msg)
					continue
				}

				// Initialize user config
				_, err := database.GetUserConfig(userID)
				if err != nil {
					log.Printf("Warning: Failed to initialize user config for user %d: %v\n", userID, err)
				} else {
					log.Printf("User config initialized for user %d\n", userID)
				}

				// Send welcome message
				welcomeMsg := tgbotapi.NewMessage(update.Message.Chat.ID, "Welcome! Send me an Airbnb search URL to scrape listings. Results will be added to Google Sheets.")
				bot.Send(welcomeMsg)

				// Send spreadsheet link as separate message and pin it
				spreadsheetMsg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("📊 Spreadsheet: %s", spreadsheetURL))
				sentSpreadsheetMsg, err := bot.Send(spreadsheetMsg)
				if err == nil {
					pinMsg := tgbotapi.PinChatMessageConfig{
						ChatID:              update.Message.Chat.ID,
						MessageID:           sentSpreadsheetMsg.MessageID,
						DisableNotification: false,
					}
					bot.Send(pinMsg)
				}
			case "help":
				helpText := "Commands:\n/start - Start the bot\n/help - Show this help\n/config - Configure filter settings\n\nJust send me an Airbnb search URL to scrape listings! Results will be automatically added to Google Sheets."
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, helpText)
				bot.Send(msg)
			case "config":
				// Show config with buttons
				userConfig, err := database.GetUserConfig(userID)
				if err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Error loading config: %v", err))
					bot.Send(msg)
					continue
				}

				configText := fmt.Sprintf(
					"⚙️ Current Configuration:\n\n"+
						"📄 Max Pages: %d\n"+
						"⭐ Min Reviews: %d\n"+
						"💰 Min Price: %.2f\n"+
						"💰 Max Price: %.2f\n"+
						"⭐ Min Stars: %.2f\n\n"+
						"Click buttons below to change values:",
					userConfig.MaxPages, userConfig.MinReviews, userConfig.MinPrice, userConfig.MaxPrice, userConfig.MinStars)

				// Create inline keyboard
				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("📄 Max Pages", "config_max_pages"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("⭐ Min Reviews", "config_min_reviews"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("💰 Min Price", "config_min_price"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("💰 Max Price", "config_max_price"),
					),
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("⭐ Min Stars", "config_min_stars"),
					),
				)

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, configText)
				msg.ReplyMarkup = keyboard
				bot.Send(msg)
			case "clear":
				// Clear the spreadsheet (write empty data)
				if err := writer.WriteListings([]models.Listing{}, true); err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Failed to clear spreadsheet: %v", err))
					bot.Send(msg)
				} else {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "✅ Spreadsheet cleared successfully!")
					bot.Send(msg)
				}
			default:
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /help for available commands.")
				bot.Send(msg)
			}
			continue
		}

		// Check if user is allowed (for non-command messages)
		if !allowedUserIDs[userID] {
			log.Printf("Unauthorized user attempted to use bot: %d\n", userID)
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Sorry, you are not authorized to use this bot.")
			bot.Send(msg)
			continue
		}

		// Handle URL messages
		url := strings.TrimSpace(update.Message.Text)
		if url == "" {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send me an Airbnb search URL.")
			bot.Send(msg)
			continue
		}

		// Validate URL
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Please send a valid URL starting with http:// or https://")
			bot.Send(msg)
			continue
		}

		// Add currency=USD to URL
		url = addCurrencyToURL(url)

		// Send processing message
		processingMsg := tgbotapi.NewMessage(update.Message.Chat.ID, "📝 Request received! Your request has been queued and will be processed shortly. You'll receive status updates as the scraping progresses.")
		sentMsg, err := bot.Send(processingMsg)
		if err != nil {
			log.Printf("Error sending processing message: %v\n", err)
			continue
		}

		// Save request to database
		req, err := database.CreateRequest(userID, sentMsg.MessageID, url)
		if err != nil {
			log.Printf("Error creating request: %v\n", err)
			errorMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentMsg.MessageID, fmt.Sprintf("❌ Error: Failed to create request: %v", err))
			bot.Send(errorMsg)
			continue
		}

		log.Printf("Created request ID %d for user %d\n", req.ID, userID)
	}
}

// refreshEnvVars refreshes environment variables (Windows-specific)
func refreshEnvVars() {
	// On Windows, we need to refresh environment variables from the system
	// This is a workaround for PowerShell/CMD not refreshing env vars immediately
	// Try PowerShell first (more reliable), then fall back to cmd
	// On Linux/Unix systems (like Railway), environment variables are already available
	cmd := exec.Command("powershell", "-Command", "Get-ChildItem Env: | ForEach-Object { \"$($_.Name)=$($_.Value)\" }")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to cmd (Windows)
		cmd = exec.Command("cmd", "/c", "set")
		output, err = cmd.Output()
		if err != nil {
			// On Linux/Unix, env vars are already available, so this is not an error
			// Just log a debug message and continue
			log.Printf("Note: Environment variable refresh skipped (likely running on Linux/Unix)\n")
			return
		}
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := line[:idx]
			value := line[idx+1:]
			// Remove trailing \r if present
			value = strings.TrimRight(value, "\r")
			// Only set if not already set (preserve existing env vars from current process)
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

// loadConfig loads configuration from file or returns defaults
func loadConfig(configPath string) *config.FilterConfig {
	var cfg *config.FilterConfig
	if _, err := os.Stat(configPath); err == nil {
		var err error
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			log.Printf("Warning: Failed to load config file: %v. Using defaults.\n", err)
			cfg = config.GetDefaultConfig()
		}
	} else {
		log.Println("Config file not found. Using default configuration.")
		cfg = config.GetDefaultConfig()
	}
	return cfg
}

// scrapeListings performs the scraping and filtering logic
func scrapeListings(url string, maxPages int, cfg *config.FilterConfig) ([]models.Listing, []models.Listing, error) {
	// Create scraper (using headless browser for JS-rendered content)
	rodScraper, err := scraper.NewRodScraper()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create scraper: %w", err)
	}
	defer func() {
		if err := rodScraper.Close(); err != nil {
			log.Printf("Warning: Failed to close browser: %v\n", err)
		}
	}()
	scraperInstance := scraper.Scraper(rodScraper)

	// Scrape pages
	htmlPages, err := scraperInstance.Scrape(url, maxPages)
	if err != nil {
		return nil, nil, fmt.Errorf("scraping failed: %w", err)
	}

	if len(htmlPages) == 0 {
		return nil, nil, fmt.Errorf("no HTML pages were collected")
	}

	// Parse listings
	parserInstance := parser.NewParser()
	var allListings []models.Listing

	for i, html := range htmlPages {
		listings, err := parserInstance.ParseHTML(html)
		if err != nil {
			log.Printf("Warning: Failed to parse page %d: %v\n", i+1, err)
			continue
		}
		allListings = append(allListings, listings...)
	}

	if len(allListings) == 0 {
		return nil, nil, fmt.Errorf("no listings found in the scraped HTML")
	}

	// Apply filters
	filterInstance := filter.NewFilter(cfg)
	filteredListings := filterInstance.ApplyFilters(allListings)

	return filteredListings, allListings, nil
}

// formatListingsConsole formats listings for console output
func formatListingsConsole(listings []models.Listing) {
	for i, listing := range listings {
		fmt.Printf("\n%d. %s\n", i+1, listing.Title)

		// Link
		if listing.URL != "" {
			fmt.Printf("   Link: %s\n", listing.URL)
		}

		// Price
		if listing.Price > 0 {
			currency := listing.Currency
			if currency == "" {
				currency = "THB" // Default fallback
			}
			// Format price with currency symbol
			switch currency {
			case "USD", "$":
				fmt.Printf("   Price: $%.2f\n", listing.Price)
			case "EUR", "€":
				fmt.Printf("   Price: €%.2f\n", listing.Price)
			case "THB", "฿":
				fmt.Printf("   Price: ฿%.0f\n", listing.Price)
			case "VND", "₫":
				fmt.Printf("   Price: ₫%.0f\n", listing.Price)
			case "GBP", "£":
				fmt.Printf("   Price: £%.2f\n", listing.Price)
			default:
				fmt.Printf("   Price: %s %.2f\n", currency, listing.Price)
			}
		} else {
			fmt.Printf("   Price: Not available\n")
		}

		// Rating (stars)
		if listing.Stars > 0 {
			// Display stars with full precision (no rounding)
			fmt.Printf("   Rating: %g\n", listing.Stars)
		}

		// Review count
		if listing.ReviewCount > 0 {
			fmt.Printf("   Review count: %d\n", listing.ReviewCount)
		}
	}
}

// formatListingsTelegram formats listings for Telegram message
func formatListingsTelegram(filteredListings, allListings []models.Listing) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Found %d listings before filtering\n", len(allListings)))
	sb.WriteString(fmt.Sprintf("Found %d listings after filtering\n\n", len(filteredListings)))

	if len(filteredListings) == 0 {
		sb.WriteString("No listings match the filter criteria.")
		return sb.String()
	}

	sb.WriteString("Filtered Listings:\n")
	sb.WriteString("==================\n\n")

	for i, listing := range filteredListings {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, listing.Title))

		// Link
		if listing.URL != "" {
			sb.WriteString(fmt.Sprintf("   Link: %s\n", listing.URL))
		}

		// Price
		if listing.Price > 0 {
			currency := listing.Currency
			if currency == "" {
				currency = "THB" // Default fallback
			}
			// Format price with currency symbol
			switch currency {
			case "USD", "$":
				sb.WriteString(fmt.Sprintf("   Price: $%.2f\n", listing.Price))
			case "EUR", "€":
				sb.WriteString(fmt.Sprintf("   Price: €%.2f\n", listing.Price))
			case "THB", "฿":
				sb.WriteString(fmt.Sprintf("   Price: ฿%.0f\n", listing.Price))
			case "VND", "₫":
				sb.WriteString(fmt.Sprintf("   Price: ₫%.0f\n", listing.Price))
			case "GBP", "£":
				sb.WriteString(fmt.Sprintf("   Price: £%.2f\n", listing.Price))
			default:
				sb.WriteString(fmt.Sprintf("   Price: %s %.2f\n", currency, listing.Price))
			}
		} else {
			sb.WriteString("   Price: Not available\n")
		}

		// Rating (stars)
		if listing.Stars > 0 {
			// Display stars with full precision (no rounding)
			sb.WriteString(fmt.Sprintf("   Rating: %g\n", listing.Stars))
		}

		// Review count
		if listing.ReviewCount > 0 {
			sb.WriteString(fmt.Sprintf("   Review count: %d\n", listing.ReviewCount))
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

// splitMessage splits a message into chunks of specified size
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var parts []string
	lines := strings.Split(text, "\n")
	var current strings.Builder

	for _, line := range lines {
		if current.Len()+len(line)+1 > maxLen {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			// If a single line is too long, split it
			if len(line) > maxLen {
				for len(line) > maxLen {
					parts = append(parts, line[:maxLen])
					line = line[maxLen:]
				}
				if len(line) > 0 {
					current.WriteString(line)
					current.WriteString("\n")
				}
			} else {
				current.WriteString(line)
				current.WriteString("\n")
			}
		} else {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// addCurrencyToURL adds ?currency=USD or &currency=USD to a URL
// Always sets currency=USD, replacing any existing currency parameter
func addCurrencyToURL(urlStr string) string {
	// Parse the URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		// If parsing fails, return original URL
		log.Printf("Warning: Failed to parse URL: %v\n", err)
		return urlStr
	}

	// Always set currency=USD (will replace if it already exists)
	query := parsedURL.Query()
	query.Set("currency", "USD")
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String()
}
