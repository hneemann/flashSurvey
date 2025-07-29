let version = -1;

function reload() {
    fetch("/resultRest/?v="+version)
        .then(function (response) {
            if (response.status !== 200) {
                window.location.reload();
                return;
            }
            return response.text();
        })
        .catch(function (error) {
            alert("Netzwerkfehler");
        })
        .then(function (json) {
            let obj=JSON.parse(json)
            if (obj.Title) {
                document.getElementById("title").innerHTML = obj.Title;
            }
            if (obj.Result) {
                document.getElementById("result").innerHTML = obj.Result;
            }
            if (obj.Version) {
                version = obj.Version;
            }
            setTimeout(reload, 200);
        })
}
