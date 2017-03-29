// Copyright 2016 Mender Software AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package devadm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mendersoftware/deviceadm/client/deviceauth"
	"github.com/mendersoftware/deviceadm/model"
	"github.com/mendersoftware/deviceadm/store"
	mstore "github.com/mendersoftware/deviceadm/store/mocks"

	"github.com/mendersoftware/go-lib-micro/log"
	"github.com/mendersoftware/go-lib-micro/requestid"
	"github.com/mendersoftware/go-lib-micro/requestlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type FakeApiRequester struct {
	status int
}

func (f FakeApiRequester) Do(r *http.Request) (*http.Response, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(f.status)
	}))
	defer ts.Close()

	res, err := http.Get(ts.URL)
	return res, err
}

func devadmWithClientForTest(d store.DataStore, clientRespStatus int) DevAdmApp {
	clientGetter := func() requestid.ApiRequester {
		return FakeApiRequester{clientRespStatus}
	}
	return &DevAdm{
		db:           d,
		clientGetter: clientGetter,
		log:          log.New(log.Ctx{})}
}

func devadmForTest(d store.DataStore) DevAdmApp {
	return &DevAdm{
		db:           d,
		clientGetter: simpleApiClientGetter,
		log:          log.New(log.Ctx{})}
}

func TestDevAdmListDevicesEmpty(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevices", 0, 1, "").
		Return([]model.Device{}, nil)

	d := devadmForTest(db)

	l, _ := d.ListDevices(0, 1, "")
	assert.Len(t, l, 0)
}

func TestDevAdmListDevices(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevices", 0, 1, "").
		Return([]model.Device{{}, {}, {}}, nil)

	d := devadmForTest(db)

	l, _ := d.ListDevices(0, 1, "")
	assert.Len(t, l, 3)
}

func TestDevAdmListDevicesErr(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevices", 0, 1, "").
		Return([]model.Device{}, errors.New("error"))

	d := devadmForTest(db)

	_, err := d.ListDevices(0, 1, "")
	assert.NotNil(t, err)
}

func TestDevAdmSubmitDevice(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("PutDevice", mock.AnythingOfType("*model.Device")).
		Return(nil)

	d := devadmWithClientForTest(db, http.StatusNoContent)

	err := d.SubmitDevice(model.Device{})

	assert.NoError(t, err)
}

func TestDevAdmSubmitDeviceErr(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("PutDevice", mock.AnythingOfType("*model.Device")).
		Return(errors.New("db connection failed"))

	d := devadmWithClientForTest(db, http.StatusNoContent)

	err := d.SubmitDevice(model.Device{})

	if assert.Error(t, err) {
		assert.EqualError(t, err, "failed to put device: db connection failed")
	}
}

func makeGetDevice(id model.AuthID) func(id model.AuthID) (*model.Device, error) {
	return func(aid model.AuthID) (*model.Device, error) {
		if aid == "" {
			return nil, errors.New("unsupported device auth ID")
		}

		if aid != id {
			return nil, store.ErrDevNotFound
		}
		return &model.Device{
			ID:       id,
			DeviceId: model.DeviceID(id),
		}, nil
	}
}

func TestDevAdmGetDevice(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevice", model.AuthID("foo")).
		Return(&model.Device{ID: "foo", DeviceId: "foo"}, nil)
	db.On("GetDevice", model.AuthID("bar")).
		Return(nil, store.ErrDevNotFound)
	db.On("GetDevice", model.AuthID("baz")).
		Return(nil, errors.New("error"))

	d := devadmForTest(db)

	dev, err := d.GetDevice("foo")
	assert.NotNil(t, dev)
	assert.NoError(t, err)

	dev, err = d.GetDevice("bar")
	assert.Nil(t, dev)
	assert.EqualError(t, err, store.ErrDevNotFound.Error())

	dev, err = d.GetDevice("baz")
	assert.Nil(t, dev)
	assert.Error(t, err)
}

func TestDevAdmAcceptDevice(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevice", model.AuthID("foo")).
		Return(&model.Device{ID: "foo"}, nil)
	db.On("GetDevice", model.AuthID("bar")).
		Return(nil, store.ErrDevNotFound)
	db.On("PutDevice", mock.AnythingOfType("*model.Device")).
		Return(nil)

	d := devadmWithClientForTest(db, http.StatusNoContent)

	err := d.AcceptDevice("foo")

	assert.NoError(t, err)

	err = d.AcceptDevice("bar")
	assert.Error(t, err)
	assert.EqualError(t, err, store.ErrDevNotFound.Error())
}

func TestDevAdmDeleteDevice(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		datastoreError error
		outError       error
	}{
		"ok": {
			datastoreError: nil,
			outError:       nil,
		},
		"no device": {
			datastoreError: store.ErrDevNotFound,
			outError:       store.ErrDevNotFound,
		},
		"datastore error": {
			datastoreError: errors.New("db connection failed"),
			outError:       errors.New("failed to delete device: db connection failed"),
		},
	}

	for name, tc := range testCases {
		t.Run(fmt.Sprintf("test case: %s", name), func(t *testing.T) {

			db := &mstore.DataStore{}
			db.On("DeleteDevice",
				mock.AnythingOfType("model.AuthID"),
			).Return(tc.datastoreError)
			i := devadmForTest(db)

			err := i.DeleteDevice("foo")

			if tc.outError != nil {
				if assert.Error(t, err) {
					assert.EqualError(t, err, tc.outError.Error())
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDevAdmRejectDevice(t *testing.T) {
	db := &mstore.DataStore{}
	db.On("GetDevice", model.AuthID("foo")).
		Return(&model.Device{ID: "foo"}, nil)
	db.On("GetDevice", model.AuthID("bar")).
		Return(nil, store.ErrDevNotFound)
	db.On("PutDevice", mock.AnythingOfType("*model.Device")).
		Return(nil)

	d := devadmWithClientForTest(db, http.StatusNoContent)

	err := d.RejectDevice("foo")

	assert.NoError(t, err)

	err = d.RejectDevice("bar")
	assert.Error(t, err)
	assert.EqualError(t, err, store.ErrDevNotFound.Error())
}

func TestNewDevAdm(t *testing.T) {
	d := NewDevAdm(&mstore.DataStore{}, deviceauth.ClientConfig{})

	assert.NotNil(t, d)
}

func TestDevAdmWithContext(t *testing.T) {
	d := devadmForTest(&mstore.DataStore{})

	l := log.New(log.Ctx{})
	ctx := context.Background()
	ctx = context.WithValue(ctx, requestlog.ReqLog, l)
	dwc := d.WithContext(ctx).(*DevAdmWithContext)
	assert.NotNil(t, dwc)
	assert.NotNil(t, dwc.log)
	assert.Equal(t, dwc.log, l)
}