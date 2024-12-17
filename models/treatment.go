package models

import (
	"errors"
	"time"
)

var ErrUnknownTreatmentType = errors.New("unknown treatment type")

type Treatment interface {
	IsAfter(time time.Time) bool
	SetID(newID string)
	GetTime() time.Time
	GetID() string
	GetType() string
}

type TreatmentOptions struct {
}

type BGCheckTreatment struct {
	ID       string
	Time     time.Time
	BGMgdl   int
	BGSource string

	Carbs     *float64
	EnteredBy *string
	Insulin   *float64
	Notes     *string
}

func (b BGCheckTreatment) IsAfter(time time.Time) bool {
	return b.Time.After(time)
}

func (b BGCheckTreatment) GetTime() time.Time {
	return b.Time
}
func (b BGCheckTreatment) GetID() string {
	return b.ID
}
func (b BGCheckTreatment) GetType() string {
	return "BG Check"
}
func (b BGCheckTreatment) SetID(newID string) {
	b.ID = newID
}

type CarbTreatment struct {
	ID    string
	Time  time.Time
	Carbs float64

	BGMgdl    *int
	BGSource  *string
	EnteredBy *string
	Insulin   *float64
	Notes     *string
}

func (c CarbTreatment) IsAfter(time time.Time) bool {
	return c.Time.After(time)
}
func (c CarbTreatment) GetTime() time.Time {
	return c.Time
}
func (c CarbTreatment) GetID() string {
	return c.ID
}
func (c CarbTreatment) GetType() string {
	return "Carbs"
}

func (c CarbTreatment) SetID(newID string) {
	c.ID = newID
}

// type Treatment struct {
// 	ID             string    `json:"_id"`
// 	Time           time.Time `json:"created_at"`
// 	EnteredBy      string    `json:"enteredBy,omitempty"`
// 	EventType      string    `json:"eventType"`
// 	Insulin        float64   `json:"insulin,omitempty"`
// 	UtcOffset      int       `json:"utcOffset,omitempty"`
// 	Carbs          float64   `json:"carbs,omitempty"`
// 	Glucose        float64   `json:"glucose,omitempty"`
// 	GlucoseType    string    `json:"glucoseType,omitempty"`
// 	Units          string    `json:"units,omitempty"`
// 	Duration       int       `json:"duration,omitempty"`
// 	SensorCode     string    `json:"sensorCode,omitempty"`
// 	Notes          string    `json:"notes,omitempty"`
// 	Fat            string    `json:"fat,omitempty"`
// 	Protein        string    `json:"protein,omitempty"`
// 	PreBolus       int       `json:"preBolus,omitempty"`
// 	Enteredinsulin string    `json:"enteredinsulin,omitempty"`
// 	Relative       int       `json:"relative,omitempty"`
// 	SplitExt       string    `json:"splitExt,omitempty"`
// 	SplitNow       string    `json:"splitNow,omitempty"`
// 	IsAnnouncement bool      `json:"isAnnouncement,omitempty"`
// }

// nightscout f/e (treatment drawer, "careportal" feature) POSTs with
// application/x-www-form-urlencoded; charset=UTF-8, then a while later, a
// treatment notification is sent over websocket
// eventType=BG+Check&glucose=9.2&glucoseType=Sensor&notes=from+chrome&units=mg%2Fdl&created_at=2024-12-15T13%3A17%3A30.135Z

// [
//  {
//    "_id": {"$oid": "675c7bb6d689f977f7a79473"},
//    "created_at": "2024-12-12T17:50:50.614Z",
//    "enteredBy": "xDrip4iOS",
//    "eventType": "Bolus",
//    "insulin": 5,
//    "utcOffset": 0
//  },
//  {
//    "_id": {"$oid": "675c7be1d689f977f7a794c9"},
//    "created_at": "2024-12-13T18:24:04.316Z",
//    "enteredBy": "xDrip4iOS",
//    "eventType": "Carbs",
//    "utcOffset": 0,
//    "carbs": 1
//  },
//  {
//    "_id": {"$oid": "675c7bf3d689f977f7a794f1"},
//    "created_at": "2024-12-13T18:24:04.319Z",
//    "enteredBy": "xDrip4iOS",
//    "eventType": "BG Check",
//    "utcOffset": 0,
//    "glucose": {"$numberDecimal": 135},
//    "glucoseType": "Finger: 7.5 mmol/L",
//    "units": "mg/dl"
//  },
//  {
//    "_id": {"$oid": "675c7bf3d689f977f7a794f3"},
//    "created_at": "2024-12-13T18:24:04.318Z",
//    "enteredBy": "xDrip4iOS",
//    "eventType": "Exercise",
//    "utcOffset": 0,
//    "duration": 20
//  },
//  {
//    "_id": {"$oid": "675c7bf3d689f977f7a794f5"},
//    "created_at": "2024-12-13T18:24:04.317Z",
//    "enteredBy": "xDrip4iOS",
//    "eventType": "Bolus",
//    "insulin": 1.25,
//    "utcOffset": 0
//  },
//  {
//    "_id": {"$oid": "675eceb8d689f977f7aa9af1"},
//    "created_at": "2024-12-02T08:54:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Sensor Start",
//    "utcOffset": 0,
//    "sensorCode": "MH019HA5WR"
//  },
//  {
//    "_id": {"$oid": "675ecfa8d689f977f7aa9c51"},
//    "created_at": "2024-12-15T12:46:30.679Z",
//    "enteredBy": "adam",
//    "eventType": "BG Check",
//    "utcOffset": 0,
//    "glucose": 8.9,
//    "glucoseType": "Finger",
//    "units": "mmol",
//    "notes": "notex"
//  },
//  {
//    "_id": {"$oid": "675ecfe2d689f977f7aa9cb7"},
//    "created_at": "2024-12-15T11:30:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Snack Bolus",
//    "insulin": 8,
//    "utcOffset": 0,
//    "carbs": 80,
//    "glucose": 8.9,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "2xbagel",
//    "fat": "1",
//    "protein": "2"
//  },
//  {
//    "_id": {"$oid": "675ed03ed689f977f7aa9d47"},
//    "created_at": "2024-12-15T12:40:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Meal Bolus",
//    "insulin": 0.5,
//    "utcOffset": 0,
//    "glucose": 8.8,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "salad",
//    "fat": "0",
//    "protein": "0",
//    "preBolus": -15
//  },
//  {
//    "_id": {"$oid": "675ed03ed689f977f7aa9d49"},
//    "created_at": "2024-12-15T12:25:00.000Z",
//    "eventType": "Meal Bolus",
//    "carbs": "",
//    "notes": "salad"
//  },
//  {
//    "_id": {"$oid": "675ed058d689f977f7aa9d85"},
//    "created_at": "2024-12-15T12:49:26.849Z",
//    "enteredBy": "adam",
//    "eventType": "Correction Bolus",
//    "insulin": 2,
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "test bolus"
//  },
//  {
//    "_id": {"$oid": "675ed078d689f977f7aa9db9"},
//    "created_at": "2024-12-15T12:49:59.617Z",
//    "enteredBy": "adam",
//    "eventType": "Carb Correction",
//    "utcOffset": 0,
//    "carbs": 2,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "test carb corr",
//    "fat": "1",
//    "protein": "3"
//  },
//  {
//    "_id": {"$oid": "675ed0a8d689f977f7aa9e07"},
//    "created_at": "2024-12-15T12:50:46.682Z",
//    "enteredBy": "adam",
//    "eventType": "Combo Bolus",
//    "insulin": 7.5,
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "duration": 5,
//    "notes": "tes combo bolus",
//    "fat": "2",
//    "protein": "3",
//    "preBolus": -15,
//    "enteredinsulin": "10",
//    "relative": 30,
//    "splitExt": "25",
//    "splitNow": "75"
//  },
//  {
//    "_id": {"$oid": "675ed0a8d689f977f7aa9e09"},
//    "created_at": "2024-12-15T12:35:46.682Z",
//    "eventType": "Combo Bolus",
//    "carbs": 4,
//    "notes": "tes combo bolus"
//  },
//  {
//    "_id": {"$oid": "675ed0cbd689f977f7aa9e4f"},
//    "created_at": "2024-12-15T11:50:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Announcement",
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "test announcement",
//    "isAnnouncement": true
//  },
//  {
//    "_id": {"$oid": "675ed102d689f977f7aa9eb2"},
//    "created_at": "2024-12-15T12:52:17.162Z",
//    "enteredBy": "adam",
//    "eventType": "Note",
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "duration": 15,
//    "notes": "15 minute test note"
//  },
//  {
//    "_id": {"$oid": "675ed11dd689f977f7aa9ed5"},
//    "created_at": "2024-12-15T10:52:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Question",
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "test question"
//  },
//  {
//    "_id": {"$oid": "675ed140d689f977f7aa9f1b"},
//    "created_at": "2024-12-15T11:00:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Exercise",
//    "utcOffset": 0,
//    "duration": 10,
//    "notes": "test 10m exercise"
//  },
//  {
//    "_id": {"$oid": "675ed165d689f977f7aa9f53"},
//    "created_at": "2024-12-15T12:00:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Site Change",
//    "utcOffset": 0,
//    "glucose": 8.7,
//    "glucoseType": "Sensor",
//    "units": "mmol",
//    "notes": "test pump site change"
//  },
//  {
//    "_id": {"$oid": "675ed1b1d689f977f7aa9fd6"},
//    "created_at": "2024-12-15T12:15:00.000Z",
//    "enteredBy": "adam",
//    "eventType": "Sensor Change",
//    "utcOffset": 0,
//    "sensorCode": "unk",
//    "notes": "test sensor insertion"
//  }
//]
