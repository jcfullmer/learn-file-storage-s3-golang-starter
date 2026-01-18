package main

import (
	"database/sql"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	ContentType := header.Header.Get("Content-Type")
	if ContentType != "image/jpeg" && ContentType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "please upload a jpg or png file.", fmt.Errorf("wrong Content-Type"))
		return
	}
	extensions, err := mime.ExtensionsByType(ContentType)
	if err != nil || len(extensions) == 0 {
		respondWithError(w, http.StatusBadRequest, "Unsupported file type", err)
		return
	}
	ext := extensions[0]
	dbVideo, err := cfg.db.GetVideo(videoID)
	if err == sql.ErrNoRows {
		respondWithError(w, http.StatusBadRequest, "No video by that ID", err)
		return
	} else if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video from database", err)
		return
	}
	if userID != dbVideo.UserID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", fmt.Errorf("User %c is not the video owner.", userID))
		return
	}
	fileName := videoIDString + ext
	tnFilePath := filepath.Join(cfg.assetsRoot, fileName)
	thumbnailFile, err := os.Create(tnFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create thumbnail file", err)
		return
	}
	defer thumbnailFile.Close()
	io.Copy(thumbnailFile, file)
	thumbnailurl := fmt.Sprintf("http://localhost:%v/assets/%v", cfg.port, fileName)
	dbVideo.ThumbnailURL = &thumbnailurl
	err = cfg.db.UpdateVideo(dbVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save thumbnail to database", err)
		return
	}

	respondWithJSON(w, http.StatusOK, dbVideo)
}
