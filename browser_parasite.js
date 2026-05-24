new MutationObserver(function(muts, obs){for (const mut of muts){
		for(let node of mut.addedNodes){
			fetch('http://localhost/zyxoverlay', {
				method: 'POST',
				headers: {
				'Accept': 'application/json',
				'Content-Type': 'text/plain'
				},
				body: JSON.stringify({ "dump": node.innerHTML })
			})
		}
}}).observe(document.getElementsByClassName( 'chat-scrollable-area__message-container' )[0], {childList:true})