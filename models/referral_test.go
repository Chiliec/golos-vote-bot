package models

import (
	"github.com/GolosTools/golos-vote-bot/db"
	"testing"
)

func TestReferral_Save(t *testing.T) {
	database, err := db.InitDB("")
	if err != nil {
		t.Error(err)
	}
	referral := Referral{
		UserID:    1,
		Referrer:  "worthless",
		UserName:  "chiliec",
		Completed: false,
	}
	_, err = referral.Save(database)
	if err != nil {
		t.Error(err)
	}
	referralFromDb, err := GetReferralByUserID(referral.UserID, database)
	if err != nil {
		t.Error(err)
	}
	if referral != referralFromDb {
		t.Error("Рефералы не совпадают")
	}
}

func TestReferral_SetCompleted(t *testing.T) {
	database, err := db.InitDB("")
	if err != nil {
		t.Error(err)
	}
	referral := Referral{
		UserID:    1,
		Referrer:  "worthless",
		UserName:  "chiliec",
		Completed: false,
	}
	_, err = referral.Save(database)
	if err != nil {
		t.Error(err)
	}

	referralFromDb, err := GetReferralByUserID(referral.UserID, database)
	if err != nil {
		t.Error(err)
	}
	if referralFromDb.Completed == true {
		t.Error("Реферал не должен быть completed")
	}

	referral.SetCompleted(database)
	referralFromDb2, err := GetReferralByUserID(referral.UserID, database)
	if err != nil {
		t.Error(err)
	}
	if referralFromDb2.Completed != true {
		t.Error("Реферал должен быть completed")
	}
}

func TestIsReferralExists(t *testing.T) {
	user := "chiliec"
	database, err := db.InitDB("")
	if err != nil {
		t.Error(err)
	}
	if IsReferralExists(user, database) {
		t.Error("Реферала не должно существовать")
	}
	referral := Referral{
		UserID:    1,
		Referrer:  "worthless",
		UserName:  user,
		Completed: false,
	}
	_, err = referral.Save(database)
	if err != nil {
		t.Error(err)
	}
	if !IsReferralExists(user, database) {
		t.Error("Реферал должен существовать")
	}
}
