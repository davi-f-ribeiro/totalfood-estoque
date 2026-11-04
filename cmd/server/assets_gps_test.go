package main

import (
 "math"
 "testing"
 "time"
)

func TestValidAssetGPS(t *testing.T) {
 now:=time.Now().UTC()
 valid:=assetGPSRequest{OperationID:"gps-1",Latitude:-3.7319,Longitude:-38.5267,AccuracyM:12,CapturedAt:now,Source:"MOVE"}
 if !validAssetGPS(valid){t.Fatal("valid capture rejected")}
 cases:=[]assetGPSRequest{}
 p:=valid;p.Latitude=91;cases=append(cases,p)
 p=valid;p.Longitude=-181;cases=append(cases,p)
 p=valid;p.AccuracyM=-1;cases=append(cases,p)
 p=valid;p.Latitude=math.NaN();cases=append(cases,p)
 p=valid;p.Source="UNKNOWN";cases=append(cases,p)
 p=valid;p.OperationID=" ";cases=append(cases,p)
 p=valid;p.CapturedAt=now.Add(-25*time.Hour);cases=append(cases,p)
 for i,p:=range cases{if validAssetGPS(p){t.Fatalf("invalid GPS accepted: %d",i)}}
}
