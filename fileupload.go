package fileupload

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"cloud.google.com/go/storage"
	"github.com/kennygrant/sanitize"
)

type File struct {
	FileName string `json:"FileName"`
	URL      string `json:"URL"`
}

func FromRequestToFile(req *http.Request, path string) (string, string, error) {
	req.ParseMultipartForm(32)
	file, handler, err := req.FormFile("file")
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	filename := getValidFileName(path, handler.Filename)
	fullpath := path + filename
	f, err := os.OpenFile(fullpath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	_, err = io.Copy(f, file)
	if err != nil {
		return "", "", err
	}
	return filename, fullpath, nil
}

func FromRequestToGoogleBucket(req *http.Request, bucketName string) (string, string, error) {
	req.ParseMultipartForm(32)
	file, handler, err := req.FormFile("file")
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		// TODO: Handle error.
	}
	bkt := client.Bucket(bucketName)

	filename := handler.Filename
	// fullpath := path + filename

	obj := bkt.Object(filename)
	if err != nil {
		return "", "", err
	}
	w := obj.NewWriter(ctx)
	_, err = io.Copy(w, file)
	if err != nil {
		return "", "", err
	}

	if err := w.Close(); err != nil {
		return "", "", err
	}

	return filename, bucketName, nil
}

func FromBuffer(name string, path string, body io.Reader) (string, string, error) {
	filename := getValidFileName(path, name)
	fullpath := path + filename
	f, err := os.OpenFile(fullpath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	// we read from tee reader as it hasn't already done its scan
	_, err = io.Copy(f, body)
	if err != nil {
		return "", "", err
	}
	return filename, fullpath, nil
}

func getValidFileName(path string, filename string) string {
	return getValidFileNameWithDupIndex(path, filename, 0)
}

func getValidFileNameWithDupIndex(path string, filename string, duplicateIndex int) string {
	filename = sanitize.Path(filename)
	dupStr := ""
	if duplicateIndex > 0 {
		dupStr = "" + strconv.Itoa(duplicateIndex) + "-"
	}
	fullpath := path + dupStr + filename

	// path doesn't exist so we can return this path
	if _, err := os.Stat(fullpath); os.IsNotExist(err) {
		return dupStr + filename
	}

	//otherwise increase file index and
	duplicateIndex++
	return getValidFileNameWithDupIndex(path, filename, duplicateIndex)
}

type operation struct {
	Operation string                 `json:"operation"`
	Params    map[string]interface{} `json:"params"`
}

type operations struct {
	Ops []*operation
}

func (o *operation) addParam(key string, val interface{}) {
	if o.Params == nil {
		o.Params = make(map[string]interface{})
	}
	o.Params[key] = val
}

func (o *operations) add(op string) {
	if o.Ops == nil {
		o.Ops = make([]*operation, 0)
	}
	o.Ops = append(o.Ops, &operation{
		Operation: op,
	})
}

func (o *operations) last() *operation {
	return o.Ops[len(o.Ops)-1]
}

func ProcessedImageFromRequest(req *http.Request, imageType string, maxWidth int, quality int, convert bool) (*http.Response, error) {
	err := req.ParseMultipartForm(32)
	if err != nil {
		return nil, err
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return ProcessedImage(file, imageType, maxWidth, quality, convert)
}

func ProcessedImage(r io.Reader, imageType string, maxWidth int, quality int, convert bool) (*http.Response, error) {
	ops := &operations{}

	originalImageType := "jpg"
	if convert {
		ops.add("convert")

		// converting
		if imageType == "jpg" {
			imageType = "jpeg"
			originalImageType = "png"
		} else if imageType == "png" {
			originalImageType = "jpg"
		}
		ops.last().addParam("type", imageType)
	}

	ops.add("fit")
	// ops.last().addParam("rotate", "0")
	// ops.last().addParam("background", "255,255,255")
	ops.last().addParam("width", maxWidth)  //absolute max
	ops.last().addParam("height", maxWidth) // dont need its ratio based
	// ops.last().addParam("stripmeta", true) // dont need its ratio based
	ops.last().addParam("quality", quality)
	// ops.last().addParam("compression", quality)
	bOps, err := json.Marshal(ops.Ops)
	if err != nil {
		return nil, err
	}
	endpoint := "https://images.nerdy.co.nz/pipeline?operations=" + url.QueryEscape(string(bOps))
	// endpoint = "https://images.nerdy.co.nz/fit?width=200&height=200"

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", "filename_placeholder."+originalImageType)
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(fw, r)
	if err != nil {
		return nil, err
	}
	err = w.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", endpoint, &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return res, nil
}
