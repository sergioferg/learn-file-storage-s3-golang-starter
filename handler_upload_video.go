package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "Video doesn't exist", err)
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Couldn't retrieve video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Invalid user id", err)
		return
	}

	fmt.Println("uploading video with ID", videoID, "by user", userID)

	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Missing/Invalid Content-Type header", nil)
		return
	}
	if header.Header.Get("Content-Type") == "" || (mediatype != "video/mp4") {
		respondWithError(w, http.StatusBadRequest, "Missing/Invalid Content-Type header", nil)
		return
	}

	tmpFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't make new video file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't make new video file", err)
		return
	}

	processedFilepath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't process video", err)
		return
	}
	processedFile, err := os.Open(processedFilepath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't open processed video", err)
		return
	}
	defer processedFile.Close()

	randByte := make([]byte, 32)
	rand.Read(randByte)
	videoRand := hex.EncodeToString(randByte)
	videoKey := fmt.Sprintf("%s.mp4", videoRand)

	videoAspectRatio, err := getVideoAspectRatio(processedFile.Name())
	var videoKeyPrefix string

	switch videoAspectRatio {
	case "16:9":
		videoKeyPrefix = "landscape"
	case "9:16":
		videoKeyPrefix = "portrait"
	default:
		videoKeyPrefix = "other"
	}

	videoKey = videoKeyPrefix + "/" + videoKey

	s3Params := &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(videoKey),
		Body:        processedFile,
		ContentType: aws.String(mediatype),
	}
	_, err = cfg.s3Client.PutObject(r.Context(), s3Params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong with aws connection", err)
		return
	}

	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, videoKey)
	video.UpdatedAt = time.Now().UTC()
	video.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video data", err)
		return
	}

	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't generate url", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)

	presignedHTTP, err := presignClient.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}

	return presignedHTTP.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}
	splitURL := strings.Split(*video.VideoURL, ",")
	if len(splitURL) < 2 {
		return video, nil
	}
	bucket, key := splitURL[0], splitURL[1]
	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Duration(5*time.Minute))
	if err != nil {
		return video, err
	}

	video.VideoURL = &presignedURL
	return video, nil
}
