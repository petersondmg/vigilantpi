package db

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

var (
	file    *os.File
	errNoDb = errors.New("db wasn't properly started")

	mutex sync.Mutex

	data map[string]interface{}

	update chan struct{}

	flush chan struct{}

	close chan struct{}

	done chan struct{}
)

func Init(db string) error {
	var err error
	file, err = os.OpenFile(db, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	data = make(map[string]interface{})
	update = make(chan struct{})
	flush = make(chan struct{})
	close = make(chan struct{})
	done = make(chan struct{})

	ticker := time.NewTicker(time.Minute * 1)
	ever := true
	persisted := true

	if err := json.NewDecoder(file).Decode(&data); err != nil && err != io.EOF {
		return err
	}

	go func() {
		persist := func() {
			if persisted {
				return
			}
			file.Seek(0, 0)
			if err := json.NewEncoder(file).Encode(data); err != nil {
				panic(err)
			}
			persisted = true
		}

		for ever {
			select {
			case <-update:
				persisted = false

			case <-close:
				ticker.Stop()
				ever = false
				persist()
				file.Close()
				done <- struct{}{}

			case <-ticker.C:
				persist()

			case <-flush:
				persist()
			}
		}
	}()

	return nil
}

func set(key string, value interface{}) error {
	if file == nil {
		return errNoDb
	}
	mutex.Lock()
	defer mutex.Unlock()
	data[key] = value
	update <- struct{}{}
	return nil
}

func get(key string) interface{} {
	if file == nil {
		return nil
	}
	mutex.Lock()
	defer mutex.Unlock()
	return data[key]
}

func Set(key string, value string) error {
	return set(key, value)
}

func Flush() {
	flush <- struct{}{}
}

func Get(key string) string {
	data := get(key)
	if data == nil {
		return ""
	}
	return data.(string)
}

func Del(key string) error {
	if file == nil {
		return errNoDb
	}
	mutex.Lock()
	defer mutex.Unlock()
	delete(data, key)
	update <- struct{}{}
	return nil
}

func GetArray(keys ...string) (found []string) {
	for _, key := range keys {
		found = append(found, strArray(get(key))...)
	}
	return
}

func strArray(data interface{}) (found []string) {
	if data == nil {
		return nil
	}
	for _, entry := range data.([]interface{}) {
		found = append(found, entry.(string))
	}
	return
}

func SetArray(key string, data []string) error {
	return set(key, strToIArray(data))
}

func strToIArray(data []string) []interface{} {
	idata := make([]interface{}, len(data))
	for i, val := range data {
		idata[i] = val
	}
	return idata
}

func AppendArray(key string, values ...string) error {
	if file == nil {
		return errNoDb
	}
	mutex.Lock()
	defer mutex.Unlock()
	data[key] = strToIArray(append(strArray(data[key]), values...))
	update <- struct{}{}
	return nil
}

func RemoveFromArray(key, value string) error {
	if file == nil {
		return errNoDb
	}
	mutex.Lock()
	defer mutex.Unlock()
	var newarr []interface{}
	for _, v := range strArray(data[key]) {
		if v == value {
			continue
		}
		newarr = append(newarr, v)
	}
	data[key] = newarr
	update <- struct{}{}
	return nil
}

func Close() {
	if file == nil {
		return
	}
	close <- struct{}{}
	<-done
}
