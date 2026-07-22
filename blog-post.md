# PHC Remote: eine moderne iOS-App für PEHA/Honeywell PHC

Hallo zusammen,

ich möchte euch ein kleines, aber inzwischen sehr brauchbares Projekt
vorstellen: **PHC Remote**, eine native iOS-App für iPhone und iPad, mit der
eine PEHA/Honeywell-PHC-Anlage direkt im lokalen Netzwerk gesteuert werden kann.
Die App ist größtenteils vibecoded mit Claude und Codex, und als moderner Ersatz für die alte offizielle **PHC Home Control** iOS-App entstanden.

Wichtig vorweg: Es gibt keinen Cloud-Dienst, keinen Account und keine fremden Server. Die App spricht direkt mit dem **STM v3** im LAN.



_Warum mache ich das überhaupt?_ Zweierlei, erstmal wollte ich lernen, wie man eine ganz einfache iOS App entwickelt. Zweitens: Die offizielle App funktioniert grundsätzlich, fühlt sich auf aktuellen Geräten aber deutlich gealtert an und ist auf größeren Displays nicht besonders angenehm.
Ich wollte eine schlichte, schnelle SwiftUI-App, die die vorhandene Installation automatisch ausliest und danach die wichtigsten Dinge zuverlaessig steuert: Licht, Steckdosen, Rollläden, Zentralbefehle.

Aus einem Wochenendprojekt ist dann etwas mehr Reverse Engineering geworden.


Das Projekt beruht auf drei Quellen, die unterschiedliche Fragen beantwortet
haben.

#### 1. Mitschnitt der alten iOS-App

Der wichtigste Einstieg war ein Mitschnitt des Datenverkehrs der offiziellen
iOS-App zum STM, unter anderem mit `mitmproxy`. Dadurch war klar, wie die App überhaupt mit dem Steuermodul spricht:

```text
POST http://<stm-ip>:6680/
XML-RPC über plain HTTP
keine Authentifizierung im LAN
```

Die offizielle App nutzt im normalen LAN-Betrieb nur wenige XML-RPC-Methoden:

```text
service.stm.whoAreYou
service.stm.readFile
service.stm.sendTelegram
service.stm.simInputEvent
```

Damit waren die großen Fragen beantwortet: Port, Transport, Pfad,
Authentifizierung, Parameter-Reihenfolge und die Tatsache, dass das iPhone keine rohen RS-485-Frames sendet.
Es übergibt dem STM nur eine Busadresse und einen Inhalt; das STM kümmert sich um die eigentliche Buskommunikation.

#### 2. Projektdatei vom STM

`readFile` liefert die Anlagenkonfiguration als ZIP-Archiv. Darin liegen unter anderem:

```text
project.ppfx  Hardware / Kanäle / Visualisierung
project.tpfx  Logik / Automationen
project.cpfx  Komfort-/UI-Gruppierungen
```

für die App reicht aktuell vor allem `project.ppfx`: Daraus werden Stockwerke, Kategorien und Geräte erzeugt. Die Namen der Kanäle folgen in unserer Anlage einem Schema wie:

```text
2.EG : Licht > Flur
2.EG : Rollo > Bad heben
2.EG : Rollo > Bad senken
```

Lichter und Steckdosen kommen aus Ausgangsmodulen, Rollläden werden über die zugeordneten Eingangskanäle `heben`/`senken` gefunden.

Ein kleiner Stolperstein: Das ZIP vom STM gesendet setzt in den Einträgen das General-Purpose-Flag Bit 3.
Die Größen der einzelnen Dateien in der ZIP stehen also nicht im lokalen Header,
sondern im nachgelagerten Data Descriptor; außerdem sind die Einträge rohes DEFLATE.
Ein handgeschriebener ZIP-Parser war dafür keine gute Idee. Gelöst ist es jetzt sauber mit **ZIPFoundation**, das das zentrale Verzeichnis am Ende des Archivs auswertet.

#### 3. Handbuch und dekompilierte PHC-Systemsoftware

Der Mitschnitt der alten App erklärt den LAN-Weg zum STM. für viele
Befehlscodes ist aber die **PHC-Systemsoftware** wertvoller:
In den dekompilierten Dateien wie `modules.xml`, `functions.xml`, `ModulLinks.xml` und `EXT_*.xml` steckt sehr viel Struktur.

Daraus lässt sich zum Beispiel ablesen:

- welche Module welche Ausgangs-/Eingangskanäle haben,
- welcher Kanaltyp welche Funktionsliste benutzt,
- welche `com`-Codes für Ein/Aus, Dimmen, JRM/Rollladen usw. existieren,
- welche Befehle Zusatzparameter wie Laufzeit, Dimmzeit oder Zielwert brauchen,
- und bei Extended-Modulen, wie die längeren Payloads aufgebaut sind.

Das Handbuch hilft beim Gegenprüfen der Bedeutung und Reihenfolge dieser
Befehle. Kurz gesagt: Der Mitschnitt hat gezeigt, **wie** mit dem STM gesprochen wird; Handbuch und Systemsoftware erklären zu einem großen Teil, **was** die einzelnen Modulbefehle bedeuten.

## Was funktioniert heute

Das ist auf echter STM-v3-Hardware getestet:

- Verbindung zum STM im lokalen Netzwerk
- Projekt vom STM laden, ZIP extrahieren und `project.ppfx` parsen
- Projekt lokal cachen, damit die App beim nächsten Start sofort da ist
- Stockwerke anzeigen
- Geräte nach Kategorien gruppieren, z. B. Licht, Rolllaeden, Steckdosen
- Lichter schalten
- Steckdosen schalten
- Zustand von Licht/Steckdosen pollen, einmal pro Ausgangsmodul alle 2,5 Sekunden
- physische Schalter werden dadurch in der App nachgezogen
- Rollläden hoch/runter/stoppen
- Zentralbefehle bzw. virtuelle Eingangsaktionen als Buttons
- Favoriten anlegen und sortieren
- iPhone-Layout mit klassischer Navigation
- iPad-Layout mit Seitenleiste und Detailansicht
- Deutsch/Englisch für die App-Oberfläche
- Demo-Modus ohne Hardware

Licht und Steckdosen laufen über:

```text
sendTelegram(0, 0x40 | dip, (channel << 5) | com)

com 2 = ein
com 3 = aus
com 6 = umschalten
```

Das Polling nutzt ebenfalls `sendTelegram`: Ein Aufruf pro AMD-Modul liefert
eine Bitmaske, in der die Ausgangszustände aller Kanäle stecken.

Rollläden werden in der App nicht direkt über JRM-Ausgangstelegramme gefahren.
Stattdessen simuliert die App die vorhandenen Tastereingaben:

```text
simInputEvent(stm, emd_module, channel, event_type, key_type = 4)
```

Auf der getesteten Anlage gilt:

- kurzer Tastimpuls auf `senken` startet Abwärtsbewegung,
- kurzer Tastimpuls auf `heben` startet Aufwärtsbewegung,
- langer Tastendruck stoppt die Bewegung.

Das ist etwas näher an der realen Verdrahtung der Anlage und vermeidet, dass
die App die Logik aus `project.tpfx` komplett nachbauen muss.

## Was noch nicht funktioniert

### Dimmer

Dimmer sind noch **nicht** real implementiert. Im Demo-Modus gibt es zwar einen Slider, aber bei einer echten STM-Verbindung erzeugt der Parser derzeit keine Dimmergeraete, und `setBrightness` ist nur ein Ein/Aus-Stub.

Was inzwischen gut verstanden ist: Die Befehlscodes aus der Systemsoftware.
Klassische Dimmerkanaele (`DIM_AUS`, `DIM_AUS_EH`) nutzen wie andere Ausgaenge ein `shift="5"`-Schema:

```text
content = (channel << 5) | com
```

Wichtige Codes:

```text
com 2   ein
com 4   aus
com 5   umschalten
com 7   in Gegenrichtung dimmen
com 8   heller dimmen
com 9   dunkler dimmen
com 22  Dimmwert + Dimmzeit setzen
```

Bei Extended-/DALI-Dimmern gibt es zusätzlich längere Payloads aus
`EXT_DIM_AUS_EXT.xml`, z. B. mit Zielwert und Zeitglied.

Was noch fehlt, ist ein Mitschnitt gegen echte Dimmer-Hardware. Offen sind vor allem:

- die numerische Busadress-Klasse für DIM-Module im STM-LAN-Aufruf,
- wie `sendTelegram` im XML-RPC-Pfad mehrteilige Payloads übergibt,
- wie Zielwert/Zeit in der echten Rückmeldung skaliert werden,
- und wie die offizielle App Dimmerzustand liest bzw. darstellt.

Wer eine PHC-Anlage mit Dimmer hat und einen kurzen Mitschnitt der offiziellen App liefern kann, wäre sehr hilfreich.

### Rollladen-Position

PHC liefert für die hier getesteten Rolllaeden keine Positions- oder
Bewegungsrueckmeldung. Die App kann also nicht seriös "37 Prozent offen"
anzeigen. Sie zeigt stattdessen kurz an, dass der Befehl gesendet wurde.

### Jalousie-Lamellen

Die Systemsoftware beschreibt JRM-Tippbetrieb für Jalousien. Die App hat dafür einen experimentellen Ansatz über kurze simulierte Tastimpulse, aber das ist nicht auf echter Jalousie-Hardware verifiziert. Normale Rolllaeden sind getestet;
Lamellenverstellung braucht noch Rueckmeldung aus einer passenden Anlage.

### Projektaktualisierung

Die Projektstruktur wird aus dem Cache geladen und bleibt dann stabil. Wenn in der PHC-Systemsoftware etwas an der Installation geändert wurde, muss man in der App aktuell manuell "Reload from STM" auslösen. Ein automatischer
Hintergrundabgleich ist noch nicht eingebaut.

### Testabdeckung anderer Anlagen

Getestet ist das Ganze auf einer realen STM-v3-Anlage. Andere Firmwarestände, Modulkombinationen und insbesondere Dimmer-/DALI-Konfigurationen brauchen noch mehr Tests.

______

Die App ist eine native SwiftUI-App für iOS 17+:

- SwiftUI für iPhone und iPad
- `@Observable` Store für App-Zustand und Befehle
- `PHCClient` als Transport-Abstraktion
- `STMv3Client` für die echte XML-RPC-Kommunikation
- `MockPHCClient` für Demo-Modus und Entwicklung
- ZIPFoundation für die Projekt-ZIP-Datei
- XcodeGen-Projekt

Der Code ist Open Source unter **AGPL-3.0**.

Viele Grüße
