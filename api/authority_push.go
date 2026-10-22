package api

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
)

// The pushes of the authorities: a member asked to accompany someone (as
// their disciple maker or not), the answer « yes » back to who asked, and a
// member chosen as disciple maker among someone's authorities. Each phone in
// its language; a touch opens the authorities (or the disciples).

var authorityRequestTitles = map[string]string{
	"fr": "Nouvelle demande",
	"en": "New request",
	"de": "Neue Anfrage",
	"es": "Nueva solicitud",
	"ar": "طلب جديد",
	"zh": "新的请求",
	"hi": "नया अनुरोध",
}

var discipleMakerRequestBodies = map[string]string{
	"fr": "%s te demande d'être son faiseur de disciples",
	"en": "%s asks you to be their disciple maker",
	"de": "%s möchte dich als Jüngermacher",
	"es": "%s te pide que seas su discipulador",
	"ar": "%s يطلب منك أن تكون صانع التلاميذ الخاص به",
	"zh": "%s 请你做他/她的门训者",
	"hi": "%s ने तुमसे उनका चेला बनाने वाला बनने का अनुरोध किया है",
}

var authorityRequestBodies = map[string]string{
	"fr": "%s te demande de l'accompagner",
	"en": "%s asks you to accompany them",
	"de": "%s bittet dich um Begleitung",
	"es": "%s te pide tu acompañamiento",
	"ar": "%s يطلب منك أن ترافقه",
	"zh": "%s 请你在信仰路上陪伴他/她",
	"hi": "%s ने तुमसे उनका मार्गदर्शन करने का अनुरोध किया है",
}

var authorityAcceptedTitles = map[string]string{
	"fr": "Demande acceptée",
	"en": "Request accepted",
	"de": "Anfrage angenommen",
	"es": "Solicitud aceptada",
	"ar": "تم قبول الطلب",
	"zh": "请求已接受",
	"hi": "अनुरोध स्वीकार हुआ",
}

var discipleMakerAcceptedBodies = map[string]string{
	"fr": "%s est maintenant ton faiseur de disciples",
	"en": "%s is now your disciple maker",
	"de": "%s ist jetzt dein Jüngermacher",
	"es": "%s es ahora tu discipulador",
	"ar": "%s هو الآن صانع التلاميذ الخاص بك",
	"zh": "%s 现在是你的门训者",
	"hi": "%s अब तुम्हारे चेला बनाने वाले हैं",
}

var authorityAcceptedBodies = map[string]string{
	"fr": "%s t'accompagne maintenant",
	"en": "%s accompanies you now",
	"de": "%s begleitet dich jetzt",
	"es": "%s te acompaña ahora",
	"ar": "%s يرافقك الآن",
	"zh": "%s 现在陪伴你",
	"hi": "%s अब तुम्हारा मार्गदर्शन करते हैं",
}

var discipleMakerChosenTitles = map[string]string{
	"fr": "Nouveau disciple",
	"en": "New disciple",
	"de": "Neuer Jünger",
	"es": "Nuevo discípulo",
	"ar": "تلميذ جديد",
	"zh": "新的门徒",
	"hi": "नया चेला",
}

var discipleMakerChosenBodies = map[string]string{
	"fr": "%s t'a choisi comme faiseur de disciples",
	"en": "%s chose you as their disciple maker",
	"de": "%s hat dich als Jüngermacher gewählt",
	"es": "%s te eligió como su discipulador",
	"ar": "%s اختارك صانعًا للتلاميذ",
	"zh": "%s 选择你做他/她的门训者",
	"hi": "%s ने तुम्हें अपना चेला बनाने वाला चुना है",
}

// pushToMember sends to the phones of [userID], [titles] and [bodies] (with
// %s: [name]) in the language of each phone, and [kind] as its type.
func (s *Server) pushToMember(userID int64, titles, bodies map[string]string, name, kind string, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	devices, err := s.store.ListDevicesOfUsers(ctx, []int64{userID})
	if err != nil {
		slog.Error("cannot read the phones of a member", "type", kind, "error", err)
		return
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	dead := []string{}
	for language, tokens := range byLanguage {
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title: titleIn(titles, language, name),
			Body:  titleIn(bodies, language, name),
			Data:  map[string]string{"type": kind, "id": strconv.FormatInt(id, 10)},
		})
		if err != nil {
			slog.Error("push to a member failed", "type", kind, "error", err)
		}
		dead = append(dead, result.Dead...)
	}
	if len(dead) > 0 {
		_ = s.store.DeleteDevicesByTokens(ctx, dead)
	}
}
