package helloworld

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"io"
	"log"
	"os"

	firebase "firebase.google.com/go"
	"firebase.google.com/go/db"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"google.golang.org/api/option"

	"github.com/google/generative-ai-go/genai"
	"github.com/line/line-bot-sdk-go/v8/linebot"
	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"
)

// Define prompt
const ImgagePrompt = `This is a receipt, and you are a secretary.  
Please organize the details from the receipt into JSON format for me. 
I only need the JSON representation of the receipt data. Eventually, 
I will need to input it into a database with the following structure:

 Receipt(ReceiptID, PurchaseStore, PurchaseDate, PurchaseAddress, TotalAmount) and 
 Items(ItemID, ReceiptID, ItemName, ItemPrice). 

Data format as follow:
- ReceiptID, using PurchaseDate, but Represent the year, month, day, hour, and minute without any separators.
- ItemID, using ReceiptID and sequel number in that receipt. 
Otherwise, if any information is unclear, fill in with "N/A". 
`

const TranslatePrompt = `
This is a JSON representation of a receipt.
Please translate the Korean characters into Chinese for me.
Using format as follow:
    Korean(Chinese)
All the Chinese will use in zh_tw.
Please response with the translated JSON.`

const SearchReceiptPrompt = `
Here is my entire shopping list %s; 
please answer my question based on this information. %s. 
Reply in zh_tw.'                
`

// Define the context
var fireDB FireDB

// LINE BOt sdk
var bot *messaging_api.MessagingApiAPI
var blob *messaging_api.MessagingApiBlobAPI
var channelToken string

// Gemni API key
var geminiKey string

// Receipt strcuture
type ScanReceipts struct {
	Receipt struct {
		ReceiptID       string `json:"ReceiptID"`
		PurchaseStore   string `json:"PurchaseStore"`
		PurchaseDate    string `json:"PurchaseDate"`
		PurchaseAddress string `json:"PurchaseAddress"`
		TotalAmount     int    `json:"TotalAmount"`
	} `json:"Receipt"`
	Items []struct {
		ItemID    string `json:"ItemID"`
		ReceiptID string `json:"ReceiptID"`
		ItemName  string `json:"ItemName"`
		ItemPrice int    `json:"ItemPrice"`
	} `json:"Items"`
}

// ReceiptData 结构体用于存储收据数据
type ReceiptData struct {
	PurchaseStore   string
	PurchaseAddress string
	ReceiptID       string
}

// Item 结构体用于存储项目信息
type Item struct {
	ItemName  string
	ItemPrice string
}

// define firebase db
type FireDB struct {
	*db.Client
}

// Define your custom struct for Gemini ChatMemory
type GeminiChat struct {
	Parts []string `json:"parts"`
	Role  string   `json:"role"`
}

func init() {
	var err error
	// Init firebase related variables
	ctx := context.Background()
	opt := option.WithCredentialsJSON([]byte(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")))
	config := &firebase.Config{DatabaseURL: os.Getenv("FIREBASE_URL")}
	app, err := firebase.NewApp(ctx, config, opt)
	if err != nil {
		log.Fatalf("error initializing app: %v", err)
	}
	client, err := app.Database(ctx)
	if err != nil {
		log.Fatalf("error initializing database: %v", err)
	}
	fireDB.Client = client

	// Init LINE Bot related variables
	geminiKey = os.Getenv("GOOGLE_GEMINI_API_KEY")
	channelToken = os.Getenv("ChannelAccessToken")
	bot, err = messaging_api.NewMessagingApiAPI(channelToken)
	if err != nil {
		log.Fatal(err)
	}

	blob, err = messaging_api.NewMessagingApiBlobAPI(channelToken)
	if err != nil {
		log.Fatal(err)
	}

	functions.HTTP("HelloHTTP", HelloHTTP)
}

func HelloHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	cb, err := webhook.ParseRequest(os.Getenv("ChannelSecret"), r)
	if err != nil {
		if err == linebot.ErrInvalidSignature {
			w.WriteHeader(400)
		} else {
			w.WriteHeader(500)
		}
		return
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(geminiKey))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	for _, event := range cb.Events {
		log.Printf("Got event %v", event)
		switch e := event.(type) {
		case webhook.MessageEvent:
			switch message := e.Message.(type) {

			// Handle only text messages
			case webhook.TextMessageContent:
				req := message.Text
				// simulating receipt data
				receiptData := ReceiptData{
					PurchaseStore:   "7-Eleven",
					PurchaseAddress: "No. 1, Songren Rd., Xinyi Dist., Taipei City 110, Taiwan (R.O.C.)",
					ReceiptID:       "202109151200",
				}

				items := []Item{
					{
						ItemName:  "Item1",
						ItemPrice: "100",
					},
					{
						ItemName:  "Item2",
						ItemPrice: "200",
					},
				}

				if req == "test" {
					itemsContents := make([]messaging_api.FlexComponentInterface, 0)
					for _, item := range items {
						itemBox := &messaging_api.FlexBox{
							Layout: "horizontal",
							Contents: []messaging_api.FlexComponentInterface{
								&messaging_api.FlexText{
									Text:  item.ItemName,
									Size:  "sm",
									Color: "#555555",
									Flex:  0,
								},
								&messaging_api.FlexText{
									Text:  "$" + item.ItemPrice,
									Size:  "sm",
									Color: "#111111",
									Align: "end",
								},
							},
						}
						itemsContents = append(itemsContents, itemBox)
					}

					flexMsg := messaging_api.FlexBubble{
						Body: &messaging_api.FlexBox{
							Layout: "vertical",
							Contents: []messaging_api.FlexComponentInterface{
								&messaging_api.FlexText{
									Text:   "RECEIPT",
									Weight: "bold",
									Color:  "#1DB446",
									Size:   "sm",
								},
								&messaging_api.FlexText{
									Text:   receiptData.PurchaseStore,
									Weight: "bold",
									Size:   "xxl",
									Margin: "md",
								},
								&messaging_api.FlexText{
									Text:  receiptData.PurchaseAddress,
									Size:  "xs",
									Color: "#aaaaaa",
									Wrap:  true,
								},
								&messaging_api.FlexSeparator{
									Margin: "xxl",
								},
								&messaging_api.FlexBox{
									Layout:   "vertical",
									Margin:   "xxl",
									Spacing:  "sm",
									Contents: itemsContents,
								},
								&messaging_api.FlexSeparator{
									Margin: "xxl",
								},
								&messaging_api.FlexBox{
									Layout: "horizontal",
									Margin: "md",
									Contents: []messaging_api.FlexComponentInterface{
										&messaging_api.FlexText{
											Text:  "RECEIPT ID",
											Size:  "xs",
											Color: "#aaaaaa",
											Flex:  0,
										},
										&messaging_api.FlexText{
											Text:  receiptData.ReceiptID,
											Color: "#aaaaaa",
											Size:  "xs",
											Align: "end",
										},
									},
								},
							},
						},
						Styles: &messaging_api.FlexBubbleStyles{
							Footer: &messaging_api.FlexBlockStyle{
								Separator: true,
							},
						},
					}

					contents := &messaging_api.FlexCarousel{
						Contents: []messaging_api.FlexBubble{flexMsg},
					}

					// Reply message
					if _, err := bot.ReplyMessage(
						&messaging_api.ReplyMessageRequest{
							ReplyToken: e.ReplyToken,
							Messages: []messaging_api.MessageInterface{
								&messaging_api.FlexMessage{
									Contents: contents,
									AltText:  "請到手機上查看名片資訊",
								},
							},
						},
					); err != nil {
						log.Print(err)
						return
					}

					return
				}
				// 取得用戶 ID
				var uID string
				switch source := e.Source.(type) {
				case webhook.UserSource:
					uID = source.UserId
				case webhook.GroupSource:
					uID = source.UserId
				case webhook.RoomSource:
					uID = source.UserId
				}

				var dbReceipts map[string]ScanReceipts
				userPath := fmt.Sprintf("receipt/%s", uID)
				err = fireDB.NewRef(userPath).Get(ctx, &dbReceipts)
				if err != nil {
					fmt.Println("load receipt failed, ", err)
				}

				// Marshall struct to json string
				jsonData, err := json.Marshal(dbReceipts)
				if err != nil {
					fmt.Println("load db failed, ", err)
				}

				qry := fmt.Sprintf(SearchReceiptPrompt, string(jsonData), req)

				// Pass the text content to the gemini-pro model for text generation
				model := client.GenerativeModel("gemini-pro")
				cs := model.StartChat()
				res, err := cs.SendMessage(ctx, genai.Text(qry))
				if err != nil {
					log.Fatal(err)
				}
				var ret string
				for _, cand := range res.Candidates {
					for _, part := range cand.Content.Parts {
						ret = ret + fmt.Sprintf("%v", part)
						log.Println(part)
					}
				}

				// Reply message
				if _, err := bot.ReplyMessage(
					&messaging_api.ReplyMessageRequest{
						ReplyToken: e.ReplyToken,
						Messages: []messaging_api.MessageInterface{
							&messaging_api.TextMessage{
								Text: ret,
							},
						},
					},
				); err != nil {
					log.Print(err)
					return
				}

			// Handle only image messages
			case webhook.ImageMessageContent:
				log.Println("Got img msg ID:", message.Id)

				// 取得用戶 ID
				var uID string
				switch source := e.Source.(type) {
				case webhook.UserSource:
					uID = source.UserId
				case webhook.GroupSource:
					uID = source.UserId
				case webhook.RoomSource:
					uID = source.UserId
				}

				// Get image content through message.Id
				content, err := blob.GetMessageContent(message.Id)
				if err != nil {
					log.Println("Got GetMessageContent err:", err)
				}
				// Read image content
				defer content.Body.Close()
				data, err := io.ReadAll(content.Body)
				if err != nil {
					log.Fatal(err)
				}

				// Pass the image content to the gemini-pro-vision model for image description
				model := client.GenerativeModel("gemini-pro-vision")
				prompt := []genai.Part{
					genai.ImageData("png", data),
					genai.Text(ImgagePrompt),
				}

				resp, err := model.GenerateContent(ctx, prompt...)
				if err != nil {
					log.Fatal(err)
				}

				// Get the returned content
				var ret string
				for _, cand := range resp.Candidates {
					for _, part := range cand.Content.Parts {
						ret = ret + fmt.Sprintf("%v", part)
						log.Println(part)
					}
				}

				// Pass the text content to the gemini-pro model for receipt translation.
				model = client.GenerativeModel("gemini-pro")
				cs := model.StartChat()
				transJson := fmt.Sprintf("%s \n --- \n %s", TranslatePrompt, ret)
				res, err := cs.SendMessage(ctx, genai.Text(transJson))
				if err != nil {
					log.Fatal(err)
				}
				var transRet string
				for _, cand := range res.Candidates {
					for _, part := range cand.Content.Parts {
						transRet = transRet + fmt.Sprintf("%v", part)
						log.Println(part)
					}
				}

				// Remove first and last line,	which are the backticks.
				lines := strings.Split(transRet, "\n")
				jsonData := strings.Join(lines[1:len(lines)-1], "\n")
				log.Println("Got jsonData:", jsonData)

				// Unmarshal json string to struct
				var receipts ScanReceipts
				err = json.Unmarshal([]byte(jsonData), &receipts)
				if err != nil {
					fmt.Println("Unmarshal failed, ", err)
				}
				userPath := fmt.Sprintf("receipt/%s", uID)
				_, err = fireDB.NewRef(userPath).Push(ctx, receipts)
				if err != nil {
					fmt.Println("load receipt failed, ", err)
				}

				// Prepare flex message
				itemsContents := make([]messaging_api.FlexComponentInterface, 0)
				for _, item := range receipts.Items {
					itemBox := &messaging_api.FlexBox{
						Layout: "horizontal",
						Contents: []messaging_api.FlexComponentInterface{
							&messaging_api.FlexText{
								Text:  item.ItemName,
								Size:  "sm",
								Color: "#555555",
								Flex:  0,
							},
							&messaging_api.FlexText{
								Text:  fmt.Sprintf("%s%d,", "$", item.ItemPrice),
								Size:  "sm",
								Color: "#111111",
								Align: "end",
							},
						},
					}
					itemsContents = append(itemsContents, itemBox)
				}

				flexMsg := messaging_api.FlexBubble{
					Body: &messaging_api.FlexBox{
						Layout: "vertical",
						Contents: []messaging_api.FlexComponentInterface{
							&messaging_api.FlexText{
								Text:   "RECEIPT",
								Weight: "bold",
								Color:  "#1DB446",
								Size:   "sm",
							},
							&messaging_api.FlexText{
								Text:   receipts.Receipt.PurchaseStore,
								Weight: "bold",
								Size:   "xxl",
								Margin: "md",
							},
							&messaging_api.FlexText{
								Text:  receipts.Receipt.PurchaseAddress,
								Size:  "xs",
								Color: "#aaaaaa",
								Wrap:  true,
							},
							&messaging_api.FlexSeparator{
								Margin: "xxl",
							},
							&messaging_api.FlexBox{
								Layout:   "vertical",
								Margin:   "xxl",
								Spacing:  "sm",
								Contents: itemsContents,
							},
							&messaging_api.FlexSeparator{
								Margin: "xxl",
							},
							&messaging_api.FlexBox{
								Layout: "horizontal",
								Margin: "md",
								Contents: []messaging_api.FlexComponentInterface{
									&messaging_api.FlexText{
										Text:  "RECEIPT ID",
										Size:  "xs",
										Color: "#aaaaaa",
										Flex:  0,
									},
									&messaging_api.FlexText{
										Text:  receipts.Receipt.ReceiptID,
										Color: "#aaaaaa",
										Size:  "xs",
										Align: "end",
									},
								},
							},
						},
					},
					Styles: &messaging_api.FlexBubbleStyles{
						Footer: &messaging_api.FlexBlockStyle{
							Separator: true,
						},
					},
				}

				contents := &messaging_api.FlexCarousel{
					Contents: []messaging_api.FlexBubble{flexMsg},
				}

				// Reply message
				if _, err := bot.ReplyMessage(
					&messaging_api.ReplyMessageRequest{
						ReplyToken: e.ReplyToken,
						Messages: []messaging_api.MessageInterface{
							&messaging_api.TextMessage{
								Text: ret,
							},
							&messaging_api.TextMessage{
								Text: transRet,
							},
							&messaging_api.FlexMessage{
								Contents: contents,
								AltText:  "請到手機上查看名片資訊",
							},
						},
					},
				); err != nil {
					log.Print(err)
					return
				}

			// Handle only video message
			case webhook.VideoMessageContent:
				log.Println("Got video msg ID:", message.Id)

			default:
				log.Printf("Unknown message: %v", message)
			}
		case webhook.FollowEvent:
			log.Printf("message: Got followed event")
		case webhook.PostbackEvent:
			data := e.Postback.Data
			log.Printf("Unknown message: Got postback: " + data)
		case webhook.BeaconEvent:
			log.Printf("Got beacon: " + e.Beacon.Hwid)
		}
	}
}
