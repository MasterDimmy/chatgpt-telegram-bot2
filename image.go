package main

import (
	"context"
	"fmt"
	"log"

	openai "github.com/sashabaranov/go-openai"
)

func handleUserDraw(userID int64, msg string) (string, bool, error) {
	ctx := context.Background()

	// Sample image by link
	reqUrl := openai.ImageRequest{
		Prompt:         msg,
		Size:           openai.CreateImageSize256x256,
		ResponseFormat: openai.CreateImageResponseFormatURL,
		N:              1,
	}

	respUrl, err := openAIClient.CreateImage(ctx, reqUrl)
	if err != nil || len(respUrl.Data) < 1 || len(respUrl.Data[0].URL) == 0 {
		log.Printf("Image creation error: %v\n", err)
		return "", false, fmt.Errorf("Image creation error: %v\n", err)
	}
	url := respUrl.Data[0].URL
	log.Println(msg, " => ", url)

	return url, false, nil

	/*
		// Example image as base64
		reqBase64 := openai.ImageRequest{
			Prompt:         msg,
			Size:           openai.CreateImageSize512x512,
			ResponseFormat: openai.CreateImageResponseFormatB64JSON,
			N:              1,
		}

		respBase64, err := openAIClient.CreateImage(ctx, reqBase64)
		if err != nil {
			fmt.Printf("Image creation error: %v\n", err)
			return
		}

		imgBytes, err := base64.StdEncoding.DecodeString(respBase64.Data[0].B64JSON)
		if err != nil {
			fmt.Printf("Base64 decode error: %v\n", err)
			return
		}

		r := bytes.NewReader(imgBytes)
		imgData, err := png.Decode(r)
		if err != nil {
			fmt.Printf("PNG decode error: %v\n", err)
			return
		}

		file, err := os.Create("example.png")
		if err != nil {
			fmt.Printf("File creation error: %v\n", err)
			return
		}
		defer file.Close()

		if err := png.Encode(file, imgData); err != nil {
			fmt.Printf("PNG encode error: %v\n", err)
			return
		}

		fmt.Println("The image was saved as example.png")
	*/
}
