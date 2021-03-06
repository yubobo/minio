/*
 * Minimalist Object Storage, (C) 2015 Minio, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package api

import (
	"net/http"
	"sort"
	"strconv"

	"encoding/xml"

	"github.com/gorilla/mux"
	"github.com/minio/minio/pkg/iodine"
	"github.com/minio/minio/pkg/storage/drivers"
	"github.com/minio/minio/pkg/utils/log"
)

const (
	maxPartsList = 1000
)

// GET Object
// ----------
// This implementation of the GET operation retrieves object. To use GET,
// you must have READ access to the object.
func (server *minioAPI) getObjectHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// verify if this operation is allowed
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	var object, bucket string
	vars := mux.Vars(req)
	bucket = vars["bucket"]
	object = vars["object"]

	metadata, err := server.driver.GetObjectMetadata(bucket, object)
	switch iodine.ToError(err).(type) {
	case nil: // success
		{
			httpRange, err := getRequestedRange(req, metadata.Size)
			if err != nil {
				writeErrorResponse(w, req, InvalidRange, acceptsContentType, req.URL.Path)
				return
			}
			switch httpRange.start == 0 && httpRange.length == 0 {
			case true:
				setObjectHeaders(w, metadata)
				if _, err := server.driver.GetObject(w, bucket, object); err != nil {
					// unable to write headers, we've already printed data. Just close the connection.
					log.Error.Println(iodine.New(err, nil))
				}
			case false:
				metadata.Size = httpRange.length
				setRangeObjectHeaders(w, metadata, httpRange)
				w.WriteHeader(http.StatusPartialContent)
				if _, err := server.driver.GetPartialObject(w, bucket, object, httpRange.start, httpRange.length); err != nil {
					// unable to write headers, we've already printed data. Just close the connection.
					log.Error.Println(iodine.New(err, nil))
				}
			}
		}
	case drivers.ObjectNotFound:
		{
			writeErrorResponse(w, req, NoSuchKey, acceptsContentType, req.URL.Path)
		}
	case drivers.ObjectNameInvalid:
		{
			writeErrorResponse(w, req, NoSuchKey, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

// HEAD Object
// -----------
// The HEAD operation retrieves metadata from an object without returning the object itself.
func (server *minioAPI) headObjectHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// verify if this operation is allowed
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	var object, bucket string
	vars := mux.Vars(req)
	bucket = vars["bucket"]
	object = vars["object"]

	metadata, err := server.driver.GetObjectMetadata(bucket, object)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			setObjectHeaders(w, metadata)
			w.WriteHeader(http.StatusOK)
		}
	case drivers.ObjectNotFound:
		{
			error := getErrorCode(NoSuchKey)
			w.Header().Set("Server", "Minio")
			w.WriteHeader(error.HTTPStatusCode)
		}
	case drivers.ObjectNameInvalid:
		{
			error := getErrorCode(NoSuchKey)
			w.Header().Set("Server", "Minio")
			w.WriteHeader(error.HTTPStatusCode)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			error := getErrorCode(InternalError)
			w.Header().Set("Server", "Minio")
			w.WriteHeader(error.HTTPStatusCode)
		}
	}
}

// PUT Object
// ----------
// This implementation of the PUT operation adds an object to a bucket.
func (server *minioAPI) putObjectHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// verify if this operation is allowed
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	var object, bucket string
	vars := mux.Vars(req)
	bucket = vars["bucket"]
	object = vars["object"]

	// get Content-MD5 sent by client and verify if valid
	md5 := req.Header.Get("Content-MD5")
	if !isValidMD5(md5) {
		writeErrorResponse(w, req, InvalidDigest, acceptsContentType, req.URL.Path)
		return
	}
	/// if Content-Length missing, throw away
	size := req.Header.Get("Content-Length")
	if size == "" {
		writeErrorResponse(w, req, MissingContentLength, acceptsContentType, req.URL.Path)
		return
	}
	/// maximum Upload size for objects in a single operation
	if isMaxObjectSize(size) {
		writeErrorResponse(w, req, EntityTooLarge, acceptsContentType, req.URL.Path)
		return
	}
	/// minimum Upload size for objects in a single operation
	//
	// Surprisingly while Amazon in their document states that S3 objects have 1byte
	// as the minimum limit, they do not seem to enforce it one can successfully
	// create a 0byte file using a regular putObject() operation
	//
	// if isMinObjectSize(size) {
	//      writeErrorResponse(w, req, EntityTooSmall, acceptsContentType, req.URL.Path)
	//	return
	// }
	sizeInt64, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		writeErrorResponse(w, req, InvalidRequest, acceptsContentType, req.URL.Path)
		return
	}
	calculatedMD5, err := server.driver.CreateObject(bucket, object, "", md5, sizeInt64, req.Body)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			w.Header().Set("ETag", calculatedMD5)
			writeSuccessResponse(w, acceptsContentType)

		}
	case drivers.ObjectExists:
		{
			writeErrorResponse(w, req, MethodNotAllowed, acceptsContentType, req.URL.Path)
		}
	case drivers.BadDigest:
		{
			writeErrorResponse(w, req, BadDigest, acceptsContentType, req.URL.Path)
		}
	case drivers.EntityTooLarge:
		{
			writeErrorResponse(w, req, EntityTooLarge, acceptsContentType, req.URL.Path)
		}
	case drivers.InvalidDigest:
		{
			writeErrorResponse(w, req, InvalidDigest, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

/// Multipart API

// New multipart upload
func (server *minioAPI) newMultipartUploadHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// handle ACL's here at bucket level
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	if !isRequestUploads(req.URL.Query()) {
		writeErrorResponse(w, req, MethodNotAllowed, acceptsContentType, req.URL.Path)
		return
	}

	var object, bucket string
	vars := mux.Vars(req)
	bucket = vars["bucket"]
	object = vars["object"]
	uploadID, err := server.driver.NewMultipartUpload(bucket, object, "")
	switch iodine.ToError(err).(type) {
	case nil:
		{
			response := generateInitiateMultipartUploadResult(bucket, object, uploadID)
			encodedSuccessResponse := encodeSuccessResponse(response, acceptsContentType)
			// write headers
			setCommonHeaders(w, getContentTypeString(acceptsContentType), len(encodedSuccessResponse))
			// write body
			w.Write(encodedSuccessResponse)
		}
	case drivers.ObjectExists:
		{
			writeErrorResponse(w, req, MethodNotAllowed, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

// Upload part
func (server *minioAPI) putObjectPartHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// handle ACL's here at bucket level
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	// get Content-MD5 sent by client and verify if valid
	md5 := req.Header.Get("Content-MD5")
	if !isValidMD5(md5) {
		writeErrorResponse(w, req, InvalidDigest, acceptsContentType, req.URL.Path)
		return
	}

	/// if Content-Length missing, throw away
	size := req.Header.Get("Content-Length")
	if size == "" {
		writeErrorResponse(w, req, MissingContentLength, acceptsContentType, req.URL.Path)
		return
	}

	/// maximum Upload size for multipart objects in a single operation
	if isMaxObjectSize(size) {
		writeErrorResponse(w, req, EntityTooLarge, acceptsContentType, req.URL.Path)
		return
	}

	sizeInt64, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		writeErrorResponse(w, req, InvalidRequest, acceptsContentType, req.URL.Path)
		return
	}

	vars := mux.Vars(req)
	bucket := vars["bucket"]
	object := vars["object"]

	uploadID := req.URL.Query().Get("uploadId")
	partIDString := req.URL.Query().Get("partNumber")

	partID, err := strconv.Atoi(partIDString)
	if err != nil {
		writeErrorResponse(w, req, InvalidPart, acceptsContentType, req.URL.Path)
	}
	calculatedMD5, err := server.driver.CreateObjectPart(bucket, object, uploadID, partID, "", md5, sizeInt64, req.Body)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			w.Header().Set("ETag", calculatedMD5)
			writeSuccessResponse(w, acceptsContentType)

		}
	case drivers.InvalidUploadID:
		{
			writeErrorResponse(w, req, NoSuchUpload, acceptsContentType, req.URL.Path)
		}
	case drivers.ObjectExists:
		{
			writeErrorResponse(w, req, MethodNotAllowed, acceptsContentType, req.URL.Path)
		}
	case drivers.BadDigest:
		{
			writeErrorResponse(w, req, BadDigest, acceptsContentType, req.URL.Path)
		}
	case drivers.EntityTooLarge:
		{
			writeErrorResponse(w, req, EntityTooLarge, acceptsContentType, req.URL.Path)
		}
	case drivers.InvalidDigest:
		{
			writeErrorResponse(w, req, InvalidDigest, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

// Abort multipart upload
func (server *minioAPI) abortMultipartUploadHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// handle ACL's here at bucket level
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	vars := mux.Vars(req)
	bucket := vars["bucket"]
	object := vars["object"]

	objectResourcesMetadata := getObjectResources(req.URL.Query())

	err := server.driver.AbortMultipartUpload(bucket, object, objectResourcesMetadata.UploadID)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			setCommonHeaders(w, getContentTypeString(acceptsContentType), 0)
			w.WriteHeader(http.StatusNoContent)
		}
	case drivers.InvalidUploadID:
		{
			writeErrorResponse(w, req, NoSuchUpload, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

// List object parts
func (server *minioAPI) listObjectPartsHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// handle ACL's here at bucket level
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	objectResourcesMetadata := getObjectResources(req.URL.Query())
	if objectResourcesMetadata.MaxParts == 0 {
		objectResourcesMetadata.MaxParts = maxPartsList
	}

	vars := mux.Vars(req)
	bucket := vars["bucket"]
	object := vars["object"]

	objectResourcesMetadata, err := server.driver.ListObjectParts(bucket, object, objectResourcesMetadata)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			response := generateListPartsResult(objectResourcesMetadata)
			encodedSuccessResponse := encodeSuccessResponse(response, acceptsContentType)
			// write headers
			setCommonHeaders(w, getContentTypeString(acceptsContentType), len(encodedSuccessResponse))
			// write body
			w.Write(encodedSuccessResponse)
		}
	case drivers.InvalidUploadID:
		{
			writeErrorResponse(w, req, NoSuchUpload, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

// Complete multipart upload
func (server *minioAPI) completeMultipartUploadHandler(w http.ResponseWriter, req *http.Request) {
	acceptsContentType := getContentType(req)
	// handle ACL's here at bucket level
	if !server.isValidOp(w, req, acceptsContentType) {
		return
	}

	decoder := xml.NewDecoder(req.Body)
	parts := &CompleteMultipartUpload{}
	err := decoder.Decode(parts)
	if err != nil {
		log.Error.Println(iodine.New(err, nil))
		writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		return
	}
	if !sort.IsSorted(completedParts(parts.Part)) {
		writeErrorResponse(w, req, InvalidPartOrder, acceptsContentType, req.URL.Path)
		return
	}

	vars := mux.Vars(req)
	bucket := vars["bucket"]
	object := vars["object"]
	objectResourcesMetadata := getObjectResources(req.URL.Query())

	partMap := make(map[int]string)
	for _, part := range parts.Part {
		partMap[part.PartNumber] = part.ETag
	}

	etag, err := server.driver.CompleteMultipartUpload(bucket, object, objectResourcesMetadata.UploadID, partMap)
	switch iodine.ToError(err).(type) {
	case nil:
		{
			response := generateCompleteMultpartUploadResult(bucket, object, "", etag)
			encodedSuccessResponse := encodeSuccessResponse(response, acceptsContentType)
			// write headers
			setCommonHeaders(w, getContentTypeString(acceptsContentType), len(encodedSuccessResponse))
			// write body
			w.Write(encodedSuccessResponse)
		}
	case drivers.InvalidUploadID:
		{
			writeErrorResponse(w, req, NoSuchUpload, acceptsContentType, req.URL.Path)
		}
	default:
		{
			log.Error.Println(iodine.New(err, nil))
			writeErrorResponse(w, req, InternalError, acceptsContentType, req.URL.Path)
		}
	}
}

/// Delete API

// Delete bucket
func (server *minioAPI) deleteBucketHandler(w http.ResponseWriter, req *http.Request) {
	error := getErrorCode(NotImplemented)
	w.WriteHeader(error.HTTPStatusCode)
}

// Delete object
func (server *minioAPI) deleteObjectHandler(w http.ResponseWriter, req *http.Request) {
	error := getErrorCode(NotImplemented)
	w.WriteHeader(error.HTTPStatusCode)
}
