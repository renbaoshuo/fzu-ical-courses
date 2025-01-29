package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/west2-online/jwch"
)

// 作息时间
var CLASS_TIME = [][2][2]int{
	{{0, 0}, {23, 59}}, // [[起始小时, 起始分钟], [结束小时, 结束分钟]]
	{{8, 20}, {9, 5}},  // 1
	{{9, 15}, {10, 0}},
	{{10, 20}, {11, 5}},
	{{11, 15}, {12, 0}},
	{{14, 0}, {14, 45}},
	{{14, 55}, {15, 40}},
	{{15, 50}, {16, 35}},
	{{16, 45}, {17, 30}},
	{{19, 0}, {19, 45}},
	{{19, 55}, {20, 40}},
	{{20, 50}, {21, 35}}, // 11
}

func main() {
	// 初始化
	var cstSh, _ = time.LoadLocation("Asia/Shanghai")
	time.Local = cstSh

	// 读入信息
	var id, password string

	fmt.Print("请输入学号: ")
	fmt.Scan(&id)
	fmt.Print("请输入密码: ")
	fmt.Scan(&password)

	// 创建学生对象
	stu := jwch.NewStudent().WithUser(id, password)

	// 登录
	err := stu.Login()
	solveErr(err)

	fmt.Println("登录成功！")

	// 获取学期列表
	terms, err := stu.GetTerms()
	solveErr(err)

	fmt.Println("========")
	fmt.Println("学期列表:", strings.Join(terms.Terms, " "))

	var needTerm string
	fmt.Print("请输入学期 (all): ")
	fmt.Scan(&needTerm)

	if needTerm == "" || needTerm == "all" {
		needTerm = "all"
	} else if !contains(terms.Terms, needTerm) {
		fmt.Println("无效学期！")
		return
	}

	// 获取校历
	calendar, err := stu.GetSchoolCalendar()
	solveErr(err)

	// 转换为 ics 格式
	cal := ics.NewCalendar()
	cal.SetMethod(ics.MethodRequest)
	cal.SetXWRCalName(fmt.Sprintf("福州大学课程表 [%s]", id))
	cal.SetTimezoneId("Asia/Shanghai")
	cal.SetXWRTimezone("Asia/Shanghai")

	if needTerm == "all" {
		for _, term := range terms.Terms {
			addTermToCalendar(stu, cal, calendar, term, terms.ViewState, terms.EventValidation)
		}
	} else {
		addTermToCalendar(stu, cal, calendar, needTerm, terms.ViewState, terms.EventValidation)
	}

	// 写入文件
	fmt.Println("========")
	fmt.Println("写入文件", needTerm+".ics")

	calendarContent := cal.Serialize()
	err = os.WriteFile(needTerm+".ics", []byte(calendarContent), 0644)
	solveErr(err)

	fmt.Println("写入成功！")
	fmt.Println("========")
}

func addTermToCalendar(stu *jwch.Student, cal *ics.Calendar, schoolCal *jwch.SchoolCalendar, term string, viewState string, eventValidation string) {
	var curTermStartDate time.Time
	var err error

	// 查找学期开始时间
	for _, item := range schoolCal.Terms {
		if item.Term == term {
			curTermStartDate, err = time.Parse("2006-01-02", item.StartDate)
			solveErr(err)
		}
	}

	if curTermStartDate.IsZero() {
		fmt.Printf("未找到学期 [%s] 开始时间！\n", term)
		return
	}

	// 使用学期开始时间的周一作为第 1 周的开始
	// 好像教务处的校历是从周一开始的，所以不用动
	dateBase := curTermStartDate

	// 获取课程表
	list, err := stu.GetSemesterCourses(term, viewState, eventValidation)
	solveErr(err)

	fmt.Printf("[%s] 找到 %d 门课程\n", term, len(list))

	addCoursesToCalendar(cal, term, list, dateBase)
}

func addCoursesToCalendar(cal *ics.Calendar, term string, courses []*jwch.Course, dateBase time.Time) {
	for _, course := range courses {
		name := course.Name
		teacher := course.Teacher
		description := "任课教师：" + teacher + "\n"

		for _, scheduleRule := range course.ScheduleRules {
			if scheduleRule.FromFullWeek { // 单独处理整周课程
				continue
			}

			location := strings.TrimPrefix(scheduleRule.Location, "旗山")
			startClass := scheduleRule.StartClass
			endClass := scheduleRule.EndClass
			startWeek := scheduleRule.StartWeek
			endWeek := scheduleRule.EndWeek
			weekday := scheduleRule.Weekday
			single := scheduleRule.Single
			double := scheduleRule.Double
			adjust := scheduleRule.Adjust

			startTime, endTime := calcClassTime(startWeek, weekday, startClass, endClass, dateBase)
			_, repeatEndTime := calcClassTime(endWeek, weekday, startClass, endClass, dateBase)
			eventIdBase := fmt.Sprintf("%s__%s_%s_%d-%d_%d_%d-%d_%s_%t_%t", term, name, teacher, startWeek, endWeek, weekday, startClass, endClass, location, single, double)

			if adjust {
				name = "[调课] " + name
				description += "本课程为调课后的课程。\n"
			}

			event := cal.AddEvent(md5Str(eventIdBase))
			event.SetCreatedTime(dateBase)
			event.SetDtStampTime(time.Now())
			event.SetModifiedAt(time.Now())
			event.SetSummary(name)
			event.SetDescription(description)
			event.SetLocation(location)
			event.SetStartAt(startTime)
			event.SetEndAt(endTime)
			if single && double { // 单双周都有
				// RRULE:FREQ=WEEKLY;UNTIL=20170101T000000Z
				event.AddRrule("FREQ=WEEKLY;UNTIL=" + repeatEndTime.Format("20060102T150405Z"))
			} else {
				// RRULE:FREQ=WEEKLY;UNTIL=20170101T000000Z;INTERVAL=2
				event.AddRrule("FREQ=WEEKLY;UNTIL=" + repeatEndTime.Format("20060102T150405Z") + ";INTERVAL=2")
			}
		}

		// 单独处理整周课程
		rawScheduleRules := strings.Split(course.RawScheduleRules, "\n")

		for _, rawScheduleRule := range rawScheduleRules {
			if rawScheduleRule == "" {
				continue
			}

			lineData := strings.Fields(rawScheduleRule)

			if strings.Contains(lineData[0], "周") { // 处理整周的课程，比如军训
				/*
					03周  星期1  -  04周  星期7
					[0] 03周
					[1] 星期1
					[2] -
					[3] 04周
					[4] 星期7
				*/
				startWeek, _ := strconv.Atoi(strings.TrimSuffix(lineData[0], "周"))
				endWeek, _ := strconv.Atoi(strings.TrimSuffix(lineData[3], "周"))
				startWeekday, _ := strconv.Atoi(strings.TrimPrefix(lineData[1], "星期"))
				endWeekday, _ := strconv.Atoi(strings.TrimPrefix(lineData[4], "星期"))

				startTime, _ := calcClassTime(startWeek, startWeekday, 0, 0, dateBase)
				_, repeatEndTime := calcClassTime(endWeek, endWeekday, 0, 0, dateBase)

				eventIdBase := fmt.Sprintf("%s__%s_%s_%d-%d_%d-%d", term, name, teacher, startWeek, endWeek, startWeekday, endWeekday)

				event := cal.AddEvent(md5Str(eventIdBase))
				event.SetCreatedTime(dateBase)
				event.SetDtStampTime(time.Now())
				event.SetModifiedAt(time.Now())
				event.SetSummary(name)
				event.SetDescription(description)
				event.SetAllDayStartAt(startTime)
				event.SetAllDayEndAt(repeatEndTime.AddDate(0, 0, 1))

				continue
			}

			// 其他课程不管
		}
	}
}

func calcClassTime(week int, weekday int, startClass int, endClass int, dateBase time.Time) (time.Time, time.Time) {
	startHour, startMinute := CLASS_TIME[startClass][0][0], CLASS_TIME[startClass][0][1]
	endHour, endMinute := CLASS_TIME[endClass][1][0], CLASS_TIME[endClass][1][1]

	startTime := dateBase.AddDate(0, 0, (week-1)*7+(weekday-1))
	startTime = time.Date(startTime.Year(), startTime.Month(), startTime.Day(), startHour, startMinute, 0, 0, time.Local)
	endTime := dateBase.AddDate(0, 0, (week-1)*7+(weekday-1))
	endTime = time.Date(endTime.Year(), endTime.Month(), endTime.Day(), endHour, endMinute, 0, 0, time.Local)

	return startTime, endTime
}

func md5Str(str string) string {
	hasher := md5.New()
	hasher.Write([]byte(str))
	fullHash := hex.EncodeToString(hasher.Sum(nil)) // 32-bit (full) hash

	return fullHash
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}

	return false
}

func solveErr(err error) {
	if err != nil {
		panic(err)
	}
}
