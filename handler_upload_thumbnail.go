package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "No video found with this ID", err)
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Couldn't fetch video metadata", err)
		return
	}
	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Invalid user id", err)
		return
	}

	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Missing/Invalid Content-Type header", nil)
		return
	}
	if header.Header.Get("Content-Type") == "" || (mediatype != "image/png" && mediatype != "image/jpeg") {
		respondWithError(w, http.StatusBadRequest, "Missing/Invalid Content-Type header", nil)
		return
	}

	randByte := make([]byte, 32)
	rand.Read(randByte)
	randVideoString := base64.RawURLEncoding.EncodeToString(randByte)

	imageFileExtension := strings.TrimPrefix(header.Header.Get("Content-Type"), "image/")
	imageFileName := randVideoString + "." + imageFileExtension
	thumbnailPath := filepath.Join(cfg.assetsRoot, imageFileName)

	imgFile, err := os.Create(thumbnailPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't make new thumbnail file", err)
		return
	}
	defer imgFile.Close()

	_, err = io.Copy(imgFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't make new thumbnail file", err)
		return
	}

	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, imageFileName)

	videoData.ThumbnailURL = &thumbnailURL
	videoData.UpdatedAt = time.Now().UTC()

	err = cfg.db.UpdateVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoData)
}
