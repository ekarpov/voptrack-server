package main

// Fixed public contract codes are translated only after transport validation.
func init() {
	rows := map[string][7]string{
		"recovery_limit":           {"Applying saved settings requires the surviving matching producer identity and no settings journal. Keep the original producer storage. A kit alone cannot recreate producer identity or prove the latest settings.", "Das Anwenden gespeicherter Einstellungen erfordert die erhaltene passende Erzeugeridentität und ein fehlendes Einstellungsjournal. Originalspeicher des Erzeugers behalten. Ein Kit allein stellt weder Erzeugeridentität her noch belegt es die neuesten Einstellungen.", "L’application des réglages enregistrés exige l’identité correspondante du producteur encore présente et l’absence de journal des réglages. Conserver le stockage d’origine du producteur. Un kit seul ne recrée pas son identité et ne prouve pas les derniers réglages.", "Aplicar los ajustes guardados requiere que se conserve la identidad correspondiente del productor y que no exista un diario de ajustes. Conserve el almacenamiento original del productor. Un kit por sí solo no recrea su identidad ni demuestra los últimos ajustes.", "Aplicar as definições guardadas exige a identidade correspondente do produtor preservada e a ausência de um diário de definições. Conserve o armazenamento original do produtor. Um kit por si só não recria a identidade nem prova as últimas definições.", "Tallennettujen asetusten käyttäminen edellyttää säilynyttä vastaavaa tuottajan tunnistetta eikä asetuslokia saa olla. Säilytä tuottajan alkuperäinen tallennustila. Pelkkä paketti ei luo tuottajan tunnistetta uudelleen eikä todista viimeisimpiä asetuksia.", "Для применения сохранённых настроек нужна сохранившаяся соответствующая идентичность производителя, а журнал настроек должен отсутствовать. Сохраните исходное хранилище производителя. Сам комплект не воссоздаёт его идентичность и не доказывает, что настройки самые последние."},
		"producer_keep":            {"Producer sets to retain (1–1000)", "Sätze beim Erzeuger behalten (1–1000)", "Ensembles à conserver côté producteur (1–1000)", "Conjuntos a conservar en el productor (1–1000)", "Conjuntos a reter no produtor (1–1000)", "Tuottajan säilyttämät kokonaisuudet (1–1000)", "Наборы, сохраняемые у производителя (1–1000)"},
		"queued":                   {"Saved before submission", "Vor Übermittlung gespeichert", "Enregistré avant envoi", "Guardado antes del envío", "Guardado antes do envio", "Tallennettu ennen lähetystä", "Сохранено до отправки"},
		"uncertain":                {"Outcome uncertain", "Ergebnis ungewiss", "Résultat incertain", "Resultado incierto", "Resultado incerto", "Tulos epävarma", "Результат неизвестен"},
		"effective":                {"Effective acknowledgement received", "Wirksamkeit bestätigt", "Application effective confirmée", "Aplicación efectiva confirmada", "Aplicação efetiva confirmada", "Voimaantulo vahvistettu", "Применение подтверждено"},
		"expired":                  {"Request history expired; past effect unknown", "Auftragsverlauf abgelaufen; frühere Wirkung unbekannt", "Historique expiré ; effet passé inconnu", "Historial caducado; efecto anterior desconocido", "Histórico expirado; efeito anterior desconhecido", "Pyyntöhistoria vanhentui; aiempi vaikutus tuntematon", "История запроса истекла; прошлый результат неизвестен"},
		"scope_changed":            {"Deployment or settings epoch changed; past effect unknown", "Deployment oder Einstellungsepoche geändert; frühere Wirkung unbekannt", "Déploiement ou époque modifié ; effet passé inconnu", "Despliegue o época de ajustes cambiado; efecto anterior desconocido", "Implementação ou época alterada; efeito anterior desconhecido", "Asennus tai asetuskausi vaihtui; aiempi vaikutus tuntematon", "Развёртывание или эпоха настроек изменены; прошлый результат неизвестен"},
		"stale_revision":           {"Stale expected revision", "Veraltete erwartete Revision", "Révision attendue périmée", "Revisión esperada obsoleta", "Revisão esperada desatualizada", "Odotettu versio vanhentui", "Ожидаемая ревизия устарела"},
		"settings_busy":            {"Not admitted: settings journal busy", "Nicht angenommen: Einstellungsjournal belegt", "Non admis : journal occupé", "No admitido: diario ocupado", "Não admitido: registo ocupado", "Ei hyväksytty: asetusloki varattu", "Не принято: журнал настроек занят"},
		"request_reused":           {"Conflicting request identity; retain for investigation", "Widersprüchliche Auftragskennung; zur Prüfung behalten", "Identité de demande en conflit ; conserver pour vérification", "Identidad de solicitud en conflicto; conservar para investigar", "Identidade de pedido em conflito; conservar para análise", "Pyynnön tunniste ristiriidassa; säilytä selvitystä varten", "Конфликт идентификатора запроса; сохранить для проверки"},
		"request_scope_mismatch":   {"Deployment or settings epoch mismatch", "Deployment oder Einstellungsepoche stimmt nicht überein", "Déploiement ou époque incompatible", "Despliegue o época incompatible", "Implementação ou época incompatível", "Asennus tai asetuskausi ei täsmää", "Не совпадает развёртывание или эпоха настроек"},
		"enabled":                  {"Enabled", "Aktiviert", "Activé", "Activado", "Ativado", "Käytössä", "Включено"},
		"ready":                    {"Available", "Verfügbar", "Disponible", "Disponible", "Disponível", "Saatavilla", "Доступно"},
		"not_configured":           {"Not configured", "Nicht eingerichtet", "Non configuré", "Sin configurar", "Não configurado", "Ei määritetty", "Не настроено"},
		"settings_unavailable":     {"Settings unavailable", "Einstellungen nicht verfügbar", "Réglages indisponibles", "Ajustes no disponibles", "Definições indisponíveis", "Asetukset eivät ole saatavilla", "Настройки недоступны"},
		"acquisition_unavailable":  {"Acquisition unavailable", "Erfassung nicht verfügbar", "Acquisition indisponible", "Adquisición no disponible", "Aquisição indisponível", "Hankinta ei ole saatavilla", "Создание копии недоступно"},
		"scheduled":                {"Scheduled", "Geplant", "Planifié", "Programado", "Agendado", "Ajastettu", "Запланировано"},
		"in_progress":              {"In progress", "In Arbeit", "En cours", "En curso", "Em curso", "Käynnissä", "Выполняется"},
		"disabled":                 {"Disabled", "Deaktiviert", "Désactivé", "Desactivado", "Desativado", "Poistettu käytöstä", "Отключено"},
		"not_started":              {"Not started", "Nicht gestartet", "Non démarré", "Sin iniciar", "Não iniciado", "Ei käynnistetty", "Не запущено"},
		"stopped":                  {"Stopped", "Gestoppt", "Arrêté", "Detenido", "Parado", "Pysäytetty", "Остановлено"},
		"settings_pending":         {"Waiting for settings", "Einstellungen ausstehend", "Réglages en attente", "Ajustes pendientes", "Definições pendentes", "Odottaa asetuksia", "Ожидает применения настроек"},
		"succeeded":                {"Succeeded", "Erfolgreich", "Réussi", "Correcto", "Concluído", "Onnistui", "Успешно"},
		"failed":                   {"Failed", "Fehlgeschlagen", "Échec", "Fallido", "Falhou", "Epäonnistui", "Ошибка"},
		"cancelled":                {"Cancelled", "Abgebrochen", "Annulé", "Cancelado", "Cancelado", "Peruutettu", "Отменено"},
		"refused_busy":             {"Refused: busy", "Abgelehnt: belegt", "Refusé : occupé", "Rechazado: ocupado", "Recusado: ocupado", "Hylätty: varattu", "Отказ: занято"},
		"refused_capacity":         {"Refused: capacity", "Abgelehnt: Kapazität", "Refusé : capacité", "Rechazado: capacidad", "Recusado: capacidade", "Hylätty: kapasiteetti", "Отказ: ограничение ёмкости"},
		"refused_settings_pending": {"Refused: settings pending", "Abgelehnt: Einstellungen ausstehend", "Refusé : réglages en attente", "Rechazado: ajustes pendientes", "Recusado: definições pendentes", "Hylätty: asetukset odottavat", "Отказ: настройки ожидают применения"},
		"refused_unavailable":      {"Refused: unavailable", "Abgelehnt: nicht verfügbar", "Refusé : indisponible", "Rechazado: no disponible", "Recusado: indisponível", "Hylätty: ei saatavilla", "Отказ: недоступно"},
		"converged":                {"At target", "Am Ziel", "Cible atteinte", "Objetivo alcanzado", "Objetivo atingido", "Tavoitteessa", "Цель достигнута"},
		"pending":                  {"Pending", "Ausstehend", "En attente", "Pendiente", "Pendente", "Odottaa", "Ожидает"},
		"blocked":                  {"Protected copies defer pruning", "Geschützte Kopien verzögern Bereinigung", "Copies protégées : nettoyage différé", "Copias protegidas: limpieza aplazada", "Cópias protegidas adiam limpeza", "Suojatut kopiot lykkäävät siivousta", "Защищённые копии задерживают очистку"},
		"degraded":                 {"Cleanup degraded", "Bereinigung beeinträchtigt", "Nettoyage dégradé", "Limpieza degradada", "Limpeza degradada", "Siivouksessa häiriö", "Проблема очистки"},
		"at_target":                {"At target", "Am Ziel", "Cible atteinte", "Objetivo alcanzado", "Objetivo atingido", "Tavoitteessa", "Цель достигнута"},
		"acquisition_in_progress":  {"Acquisition in progress", "Erfassung läuft", "Acquisition en cours", "Adquisición en curso", "Aquisição em curso", "Hankinta käynnissä", "Идёт создание копии"},
		"pruning":                  {"Pruning", "Bereinigung", "Nettoyage", "Limpieza", "Limpeza", "Siivous", "Очистка"},
		"leases":                   {"Leases", "Leases", "Baux", "Concesiones", "Concessões", "Varaukset", "Аренды"},
		"transfers":                {"Transfers", "Übertragungen", "Transferts", "Transferencias", "Transferências", "Siirrot", "Передачи"},
		"reserved":                 {"Reserved", "Reserviert", "Réservé", "Reservado", "Reservado", "Varattu", "Зарезервировано"},
		"cleanup_failed":           {"Cleanup failed", "Bereinigung fehlgeschlagen", "Échec du nettoyage", "Limpieza fallida", "Limpeza falhou", "Siivous epäonnistui", "Ошибка очистки"},
		"journal_unavailable":      {"Retention journal unavailable", "Aufbewahrungsjournal nicht verfügbar", "Journal de conservation indisponible", "Diario de retención no disponible", "Registo de retenção indisponível", "Säilytysloki ei ole saatavilla", "Журнал хранения недоступен"},
	}
	for key, values := range rows {
		for i, lang := range languages {
			messages[lang]["native_code_"+key] = values[i]
		}
	}
}
