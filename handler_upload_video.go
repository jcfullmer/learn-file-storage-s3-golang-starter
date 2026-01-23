package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const upload_limit = 1 << 30
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	dbVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "cannot get video", err)
		return
	}
	if dbVideo.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "user is not the video owner", fmt.Errorf("Unauthorized user id"))
		return
	}

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "File must be an MP4 video", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error saving file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	io.Copy(tempFile, file)
	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error getting aspect ratio", err)
		return
	}

	tempFile.Seek(0, io.SeekStart)

	randFN := make([]byte, 32)
	rand.Read(randFN)
	randFNString := base64.RawURLEncoding.EncodeToString(randFN) + ".mp4"
	var S3FileName string
	switch ratio {
	case "16:9":
		S3FileName = "landscape/" + randFNString
	case "9:16":
		S3FileName = "portrait/" + randFNString
	default:
		S3FileName = "other/" + randFNString
	}

	processedVideo, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error processing video for fast start", err)
	}
	processedFile, err := os.Open(processedVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error opening processed file", err)
		return
	}
	defer processedFile.Close()

	s3Obj := s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(S3FileName),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &s3Obj)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error uploading to aws", err)
		return
	}
	FileURL := fmt.Sprintf("%v/%v", cfg.CloudFrontURL, S3FileName)
	dbVideo.VideoURL = &FileURL
	err = cfg.db.UpdateVideo(dbVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error writing to databse", err)
		return
	}

}

func getVideoAspectRatio(filepath string) (string, error) {
	type ffprobeRes struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_streams", filepath,
	}
	cmd := exec.Command("ffprobe", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	ffprobeR := ffprobeRes{}
	if err = json.Unmarshal(out.Bytes(), &ffprobeR); err != nil {
		return "", err
	}
	width, height := ffprobeR.Streams[0].Width, ffprobeR.Streams[0].Height
	wDh := float64(width) / float64(height)
	var Ratio string
	if (wDh - 0.56) < 0.25 {
		Ratio = "9:16"
	} else if (wDh - 1.77) < 0.25 {
		Ratio = "16:9"
	} else {
		Ratio = "other"
	}
	return Ratio, nil

}

func processVideoForFastStart(filepath string) (string, error) {
	outputFilePath := filepath + ".pending"
	args := []string{
		"-i", filepath,
		"-c", "copy",
		"-movflags", "faststart", "-f", "mp4", outputFilePath,
	}
	cmd := exec.Command("ffmpeg", args...)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return outputFilePath, nil
}
